package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/user"
	"strings"
	"sync"
	"time"
)

var (
	flagSudo         bool
	flagParallel     int
	flagMesos        string
	flagDebug        bool
	flagUser         string
	flagPort         int
	flagPty          bool
	flagInterleave   bool
	flagKeyfile      string
	flagForwardAgent bool
	flagNoAgent      bool
	flagFiles        FileList
	flagTimeout      time.Duration
)

func init() {
	var defaultUser string
	if user_, err := user.Current(); err == nil && user_ != nil {
		defaultUser = user_.Username
	}

	flag.BoolVar(&flagDebug, "debug", false, "Write debug output")
	flag.StringVar(&flagMesos, "mesos", "http://leader.mesos:5050", "Address of Mesos leader")
	flag.IntVar(&flagParallel, "m", 4, "How many sessions to run in parallel")
	flag.StringVar(&flagUser, "user", defaultUser, "Remote username")
	flag.IntVar(&flagPort, "port", 22, "SSH port")
	flag.BoolVar(&flagForwardAgent, "forward-agent", false, "Forwards the local SSH agent to the remote host")
	flag.StringVar(&flagKeyfile, "key", "", "Use the specified keyfile to authenticate to the remote host")
	flag.BoolVar(&flagNoAgent, "no-agent", false, "Do not use the local ssh agent to authenticate remotely")
	flag.BoolVar(&flagSudo, "sudo", false, "Run commands as superuser on the remote machine")
	flag.BoolVar(&flagPty, "pty", false, "Run command in a pty (automatically applied with -sudo)")
	flag.DurationVar(&flagTimeout, "timeout", time.Minute, "Timeout for remote command")
	flag.BoolVar(&flagInterleave, "interleave", false, "Interleave output from each session rather than wait for it to finish")
	flag.Var(&flagFiles, "f", "Send specified file to a temporary directory before running the command.\n\tThe command will be invoked from inside the temporary directory, and the\n\tdirectory will be deleted after execution is completed.  This can be\n\tspecified multiple times.")

	flag.Usage = usage
}

func usage() {
	fmt.Printf("Usage: %s [OPTIONS] <masters|public|private|agents|all> <cmd>\n", os.Args[0])
	flag.PrintDefaults()
}

func main() {
	flag.Parse()
	args := flag.Args()
	if len(args) < 2 {
		flag.Usage()
		os.Exit(2)
	}

	if flagDebug {
		log.SetFlags(log.LstdFlags | log.Lshortfile)
	} else {
		log.SetOutput(ioutil.Discard)
	}

	msgs := log.New(os.Stderr, "mesos-ssh", log.LstdFlags)
	hosts, err := GetHosts(flagMesos, args[0], msgs)
	if err != nil {
		msgs.Fatalf("Failed to find hosts: %s", err.Error())
	}

	log.Printf("Found hosts: %s", strings.Join(hosts, ", "))

	auth, err := NewAuth(flagKeyfile, flagForwardAgent, !flagNoAgent)
	if err != nil {
		msgs.Fatalf("Failed to initialize auth: %s", err.Error())
	}

	var coll IOCollector
	if flagInterleave {
		coll = NewInterleavedIOCollector()
	} else {
		coll = NewRegularIOCollector()
	}

	sem := make(chan bool, flagParallel)
	var wg sync.WaitGroup

	cmd := NewSSHCommand(args[1], flagSudo, flagPty, flagForwardAgent, flagTimeout, flagFiles)

	for _, host := range hosts {
		remote := coll.NewRemote(host)
		ssh := NewSSHSession(host, flagUser, auth, remote)
		go func() {
			wg.Add(1)
			<-sem
			defer func() {
				sem <- true
				wg.Done()
			}()

			if err := ssh.Connect(flagPort); err != nil {
				remote.Done(err)
				return
			}

			remote.Done(ssh.Run(cmd))
			ssh.Close()
		}()
	}

	// Kick off the first N goroutines.
	log.Println("Unlocking the semaphore")
	for i := 0; i < flagParallel; i++ {
		sem <- true
	}

	// Read back results.
	log.Println("Reading the results")
	coll.Read()

	// Wait for all to be done.
	log.Println("Waiting for completion")
	wg.Wait()
	close(sem)
}

type FileList []string

func (list *FileList) String() string {
	return strings.Join(*list, "; ")
}

func (list *FileList) Set(s string) error {
	// Check whether file exists and is accessible.
	if file, err := os.Open(s); err != nil {
		return err
	} else {
		file.Close()
	}

	*list = append(*list, s)
	return nil
}
