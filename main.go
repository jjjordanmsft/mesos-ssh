package main

import (
	"flag"
	"fmt"
	"golang.org/x/crypto/ssh/terminal"
	"io/ioutil"
	"log"
	"os"
	"os/user"
	"strings"
	"sync"
	"time"
)

var (
	flagSudo       bool
	flagParallel   int
	flagMesos      string
	flagDebug      bool
	flagUser       string
	flagPort       int
	flagPty        bool
	flagInterleave bool
	flagTimeout    time.Duration
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
	flag.BoolVar(&flagSudo, "sudo", false, "Run commands as superuser on the remote machine")
	flag.BoolVar(&flagPty, "pty", false, "Run command in a pty (automatically applied with -sudo)")
	flag.DurationVar(&flagTimeout, "timeout", time.Minute, "Timeout for remote command")
	flag.BoolVar(&flagInterleave, "interleave", false, "Interleave output from each session rather than wait for it to finish")

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
	hosts, err := getHosts(args[0], msgs)
	if err != nil {
		msgs.Fatalf("Failed to find hosts: %s", err.Error())
	}

	log.Printf("Found hosts: %s", strings.Join(hosts, ", "))

	fmt.Printf("Password:")
	pw, err := terminal.ReadPassword(0)
	if err != nil {
		msgs.Fatalf("Failed to read password: %s", err.Error())
	}

	fmt.Println()
	log.Printf("Read password of length %d", len(pw))

	var coll IOCollector
	if flagInterleave {
		coll = NewInterleavedIOCollector()
	} else {
		coll = NewRegularIOCollector()
	}

	sem := make(chan bool, flagParallel)
	var wg sync.WaitGroup

	cmd := NewSSHCommand(args[1], flagSudo, flagPty, flagTimeout)

	for _, host := range hosts {
		remote := coll.NewRemote(host)
		ssh := NewSSHSessionPassword(host, flagUser, string(pw), remote)
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
}

func getHosts(spec string, msgs *log.Logger) ([]string, error) {
	if spec == "masters" {
		return GetMasters()
	}

	var result []string
	mesosClient, err := DiscoverMesos(flagMesos, msgs)
	if err != nil {
		return result, err
	}

	agents, err := mesosClient.GetAgents()
	if err != nil {
		return result, err
	}

	if spec == "agents" || spec == "all" {
		result, err = filterAgents(agents, func(ag *MesosAgent) bool { return true }), nil
		if err != nil {
			return result, err
		}

		if spec == "all" {
			masters, err := GetMasters()
			if err != nil {
				return result, err
			}

			result = append(result, masters...)
		}

		return result, nil
	} else if spec == "public" {
		return filterAgents(agents, hasPublicResource), nil
	} else if spec == "private" {
		return filterAgents(agents, func(ag *MesosAgent) bool { return !hasPublicResource(ag) }), nil
	} else {
		return result, fmt.Errorf("Invalid host spec: %s", spec)
	}
}

func filterAgents(resp *MesosAgentsResponse, f func(agent *MesosAgent) bool) []string {
	var result []string
	for _, agent := range resp.Agents {
		if f(agent) {
			result = append(result, agent.AgentInfo.Hostname)
		}
	}

	return result
}

func hasPublicResource(agent *MesosAgent) bool {
	for _, resource := range agent.AgentInfo.Resources {
		if resource.Role == "slave_public" {
			return true
		}
	}

	return false
}
