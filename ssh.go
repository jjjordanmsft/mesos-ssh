package main

import (
	"bytes"
	"fmt"
	"golang.org/x/crypto/ssh"
	"io"
	"log"
)

func RunSSH(host, user, pw, cmd string, sudo bool, msgs *log.Logger, remote RemoteIO) error {
	log.Printf("Starting connection to %s", host)
	sshConfig := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.Password(pw),
		},
	}

	connection, err := ssh.Dial("tcp", fmt.Sprintf("%s:22", host), sshConfig)
	if err != nil {
		return err
	}

	defer connection.Close()

	log.Printf("Initiating session on %s", host)
	session, err := connection.NewSession()
	if err != nil {
		return err
	}

	defer session.Close()

	tmodes := ssh.TerminalModes{
		ssh.ECHO:          0,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}

	log.Printf("Requesting pty on %s", host)
	if err := session.RequestPty("xterm", 80, 25, tmodes); err != nil {
		return err
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		return err
	}

	stderr, err := session.StderrPipe()
	if err != nil {
		return err
	}

	var cmdErr error
	if sudo {
		stdin, err := session.StdinPipe()
		if err != nil {
			return err
		}

		go writePass(host, pw, stdin, stdout, remote)
		go io.Copy(&stderrWriter{remote}, stderr)

		log.Printf("Invoking cmd on %s", host)
		cmdErr = session.Run(fmt.Sprintf("/usr/bin/sudo /bin/bash -c '%s'", cmd))
	} else {
		go io.Copy(&stdoutWriter{remote}, stdout)
		go io.Copy(&stderrWriter{remote}, stderr)

		log.Printf("Invoking cmd on %s", host)
		cmdErr = session.Run(cmd)
	}

	if cmdErr == nil {
		// Exited normally.
		log.Printf("Cmd on %s terminated normally", host)
		remote.OnExit(0)
		return nil
	} else if exitError, ok := cmdErr.(*ssh.ExitError); ok {
		// Exited with error status.
		log.Printf("Cmd on %s terminated with code %d", exitError.ExitStatus())
		remote.OnExit(exitError.ExitStatus())
		return nil
	} else {
		// Abnormally exited.
		log.Printf("Cmd on %s terminated abnormally: %s", host, cmdErr.Error())
		remote.OnExit(-1)
		return cmdErr
	}
}

func writePass(host, pw string, stdin io.Writer, stdout io.Reader, remote RemoteIO) {
	var buf bytes.Buffer
	sect := make([]byte, 32)

	for {
		n, err := stdout.Read(sect)
		if err != nil {
			log.Printf("Read error while waiting for password on %s: %s", host, err.Error())
			return
		}

		buf.Write(sect[:n])
		remote.OnStdout(sect[:n])
		if bytes.Contains(buf.Bytes(), []byte("[sudo] password for ")) {
			break
		}
	}

	log.Printf("Responding to password prompt on %s", host)
	stdin.Write([]byte(pw))
	stdin.Write([]byte{'\r'})
	io.Copy(&stdoutWriter{remote}, stdout)
}
