package main

import (
	"fmt"
	"io/ioutil"
	"net"
	"os"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/terminal"
)

type Auth struct {
	pw      *passwordMarshaller
	methods []ssh.AuthMethod
	agent   agent.Agent
}

func NewAuth(privateKey string, forwardAgent, authWithAgent bool) (*Auth, error) {
	auth := &Auth{}
	auth.pw = newPasswordMarshaller()

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

	// Lastly, add password
	auth.methods = append(auth.methods, ssh.PasswordCallback(auth.pw.getPassword))

	return auth, nil
}

func (auth *Auth) getPassword() (string, error) {
	return auth.pw.getPassword()
}

func (auth *Auth) getAuthMethods() []ssh.AuthMethod {
	return auth.methods
}

func (auth *Auth) forwardAgent(connection *ssh.Client) error {
	if auth.agent != nil {
		return agent.ForwardToAgent(connection, auth.agent)
	} else {
		return fmt.Errorf("No agent available")
	}
}

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
