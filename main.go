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
)

var (
	flagSudo     bool
	flagParallel int
	flagMesos    string
	flagDebug    bool
	flagUser     string
)

func init() {
	var defaultUser string
	if user_, err := user.Current(); err == nil && user_ != nil {
		defaultUser = user_.Username
	}

	flag.BoolVar(&flagSudo, "sudo", false, "Run commands as superuser on the remote machine")
	flag.IntVar(&flagParallel, "m", 4, "How many sessions to run in parallel")
	flag.StringVar(&flagMesos, "mesos", "http://leader.mesos:5050", "Address of Mesos leader")
	flag.BoolVar(&flagDebug, "debug", false, "Write debug output")
	flag.StringVar(&flagUser, "user", defaultUser, "Remote username")
}

func main() {
	flag.Parse()
	args := flag.Args()
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Must specify 'machines' and 'cmd' on the commandline")
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

	coll := NewRegularIOCollector()

	sem := make(chan bool, flagParallel)
	var wg sync.WaitGroup

	for _, host := range hosts {
		h := host
		remote := coll.NewRemote(h)
		go func() {
			wg.Add(1)
			<-sem
			defer func() {
				sem <- true
				wg.Done()
			}()

			RunSSH(h, flagUser, string(pw), args[1], flagSudo, msgs, remote)
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
