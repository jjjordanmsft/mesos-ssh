package main

import (
	"bytes"
	"fmt"
	"golang.org/x/crypto/ssh"
	"io"
	"log"
	"net"
	"time"
)

type SSHCommand struct {
	Command string
	Sudo    bool
	Pty     bool
	Timeout time.Duration
}

type SSHSession struct {
	Host   string
	Config *ssh.ClientConfig
	Remote *RemoteIO

	connection *ssh.Client
	password   string
}

func NewSSHCommand(cmd string, sudo, pty bool, timeout time.Duration) *SSHCommand {
	return &SSHCommand{
		Command: cmd,
		Sudo:    sudo,
		Pty:     pty,
		Timeout: timeout,
	}
}

func NewSSHSessionPassword(host, user, pw string, remote *RemoteIO) *SSHSession {
	session := NewSSHSession(host, UserPass(user, pw), remote)
	session.password = pw
	return session
}

func UserPass(user, pw string) *ssh.ClientConfig {
	return &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.Password(pw),
		},
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			return nil
		},
	}
}

func NewSSHSession(host string, config *ssh.ClientConfig, remote *RemoteIO) *SSHSession {
	return &SSHSession{
		Host:   host,
		Config: config,
		Remote: remote,
	}
}

func (sesh *SSHSession) Connect(port int) error {
	log.Printf("Starting connection to %s", sesh.Host)
	connection, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", sesh.Host, port), sesh.Config)
	if err != nil {
		return err
	}

	sesh.connection = connection
	return nil
}

func (sesh *SSHSession) Close() {
	sesh.connection.Close()
	sesh.connection = nil
}

func (sesh *SSHSession) Run(cmd *SSHCommand) error {
	log.Printf("Initiating session on %s", sesh.Host)
	session, err := sesh.connection.NewSession()
	if err != nil {
		return err
	}

	defer session.Close()

	if cmd.Sudo || cmd.Pty {
		tmodes := ssh.TerminalModes{
			ssh.ECHO:          0,
			ssh.TTY_OP_ISPEED: 14400,
			ssh.TTY_OP_OSPEED: 14400,
		}

		log.Printf("Requesting pty on %s", sesh.Host)
		if err := session.RequestPty("xterm", 80, 25, tmodes); err != nil {
			return err
		}
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		return err
	}

	stderr, err := session.StderrPipe()
	if err != nil {
		return err
	}

	timeout := time.AfterFunc(cmd.Timeout, func() {
		session.Close()
	})

	var cmdErr error
	if cmd.Sudo {
		stdin, err := session.StdinPipe()
		if err != nil {
			return err
		}

		go sesh.writePass(stdin, stdout)
		go io.Copy(&stderrWriter{sesh.Remote}, stderr)

		log.Printf("Invoking cmd on %s", sesh.Host)
		cmdErr = session.Run(fmt.Sprintf("/usr/bin/sudo /bin/bash -c '%s'", cmd.Command))
	} else {
		go io.Copy(&stdoutWriter{sesh.Remote}, stdout)
		go io.Copy(&stderrWriter{sesh.Remote}, stderr)

		log.Printf("Invoking cmd on %s", sesh.Host)
		cmdErr = session.Run(cmd.Command)
	}

	timeout.Stop()

	if cmdErr == nil {
		// Exited normally.
		log.Printf("Cmd on %s terminated normally", sesh.Host)
		sesh.Remote.Exit(0)
		return nil
	} else if exitError, ok := cmdErr.(*ssh.ExitError); ok {
		// Exited with error status.
		log.Printf("Cmd on %s terminated with code %d", exitError.ExitStatus())
		sesh.Remote.Exit(exitError.ExitStatus())
		return nil
	} else {
		// Abnormally exited.
		log.Printf("Cmd on %s terminated abnormally: %s", sesh.Host, cmdErr.Error())
		return cmdErr
	}
}

func (sesh *SSHSession) writePass(stdin io.Writer, stdout io.Reader) {
	var buf bytes.Buffer
	sect := make([]byte, 32)

	for {
		n, err := stdout.Read(sect)
		if err != nil {
			log.Printf("Read error while waiting for password on %s: %s", sesh.Host, err.Error())
			return
		}

		buf.Write(sect[:n])
		sesh.Remote.Stdout(sect[:n])
		if bytes.Contains(buf.Bytes(), []byte("[sudo] password for ")) {
			log.Printf("Responding to password prompt on %s", sesh.Host)
			stdin.Write([]byte(sesh.password))
			stdin.Write([]byte{'\r'})
			break
		}

		if buf.Len() > 64 {
			// Should be first thing. If we haven't seen it, then just
			// fuggheddaboudit.
			log.Println("No sudo prompt found in first 64 bytes, skipping.")
			break
		}
	}

	io.Copy(&stdoutWriter{sesh.Remote}, stdout)
}
