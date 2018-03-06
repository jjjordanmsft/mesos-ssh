package main

import (
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"strings"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/terminal"
)

// Manages authentication
type Auth struct {
	pw       *passwordMarshaller
	methods  []ssh.AuthMethod
	agent    agent.Agent
	password string
}

// Sets up SSH authentication methods, password input
func NewAuth(privateKey, passwordFile string, forwardAgent, authWithAgent bool) (*Auth, error) {
	auth := &Auth{}

	// Authenticate with private key?
	if privateKey != "" {
		contents, err := ioutil.ReadFile(privateKey)
		if err != nil {
			return nil, err
		}

		key, err := ssh.ParsePrivateKey(contents)
		if err != nil {
			return nil, err
		}

		auth.methods = append(auth.methods, ssh.PublicKeys(key))
	}

	// Check for an agent, first.
	if forwardAgent || authWithAgent {
		authSock := os.Getenv("SSH_AUTH_SOCK")
		if authSock != "" {
			if conn, err := net.Dial("unix", authSock); err == nil {
				auth.agent = agent.NewClient(conn)
				if authWithAgent {
					auth.methods = append(auth.methods, ssh.PublicKeysCallback(auth.agent.Signers))
				}
			} else {
				return nil, err
			}
		}
	}

	if passwordFile != "" {
		// Use the contents of the passwordFile as the password
		pw, err := ioutil.ReadFile(passwordFile)
		if err != nil {
			return nil, err
		}

		auth.password = strings.TrimSpace(string(pw))
		auth.methods = append(auth.methods, ssh.Password(auth.password))
	} else {
		// Or just prompt for the password
		auth.pw = newPasswordMarshaller()
		auth.methods = append(auth.methods, ssh.PasswordCallback(auth.pw.getPassword))
	}

	return auth, nil
}

// Prompt for password if it hasn't already been entered
func (auth *Auth) getPassword() (string, error) {
	if auth.pw == nil {
		return auth.password, nil
	} else {
		return auth.pw.getPassword()
	}
}

// Gets AuthMethods for SSH login
func (auth *Auth) getAuthMethods() []ssh.AuthMethod {
	return auth.methods
}

// Initialize SSH agent forwarding on the specified connection
func (auth *Auth) forwardAgent(connection *ssh.Client) error {
	if auth.agent != nil {
		return agent.ForwardToAgent(connection, auth.agent)
	} else {
		return fmt.Errorf("No agent available")
	}
}

// Prompts for password the first time it's asked for, returns it each
// additional time.
type passwordMarshaller struct {
	requests chan passwordRequest
}

type passwordRequest chan<- *passwordResponse
type passwordResponse struct {
	password string
	err      error
}

func newPasswordMarshaller() *passwordMarshaller {
	marshaller := &passwordMarshaller{make(chan passwordRequest)}
	go marshaller.run()
	return marshaller
}

func (pw *passwordMarshaller) getPassword() (string, error) {
	result := make(chan *passwordResponse)
	pw.requests <- passwordRequest(result)
	response := <-result
	close(result)
	return response.password, response.err
}

func (pw *passwordMarshaller) run() {
	request := <-pw.requests
	fmt.Printf("Password:")
	password, err := terminal.ReadPassword(0)
	fmt.Println()

	response := &passwordResponse{password: string(password), err: err}
	request <- response
	for {
		request := <-pw.requests
		request <- response
	}
}
