package main

import (
	"bytes"
	"fmt"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"io"
	"log"
	"net"
	"os"
	"path"
	"strings"
	"time"
)

type SSHCommand struct {
	Command      string
	Sudo         bool
	Pty          bool
	Timeout      time.Duration
	Files        []string
	ForwardAgent bool
}

type SSHSession struct {
	Host   string
	Config *ssh.ClientConfig
	Remote *RemoteIO

	connection *ssh.Client
	auth       *Auth
}

func NewSSHCommand(cmd string, sudo, pty, forwardAgent bool, timeout time.Duration, files []string) *SSHCommand {
	return &SSHCommand{
		Command:      cmd,
		Sudo:         sudo,
		Pty:          pty,
		Timeout:      timeout,
		Files:        files,
		ForwardAgent: forwardAgent,
	}
}

func NewSSHSession(host, user string, auth *Auth, remote *RemoteIO) *SSHSession {
	return &SSHSession{
		Host:   host,
		Remote: remote,
		Config: &ssh.ClientConfig{
			User: user,
			Auth: auth.getAuthMethods(),
			HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
				return nil
			},
		},
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
	if len(cmd.Files) > 0 {
		tmpdir, err := sesh.mktemp()
		if err != nil {
			return err
		}

		defer sesh.deltemp(tmpdir)
		if err := sesh.sendFiles(tmpdir, cmd.Files); err != nil {
			return err
		}

		return sesh.runCommand(cmd, tmpdir)
	} else {
		return sesh.runCommand(cmd, "")
	}
}

func (sesh *SSHSession) runCommand(cmd *SSHCommand, dir string) error {
	if cmd.ForwardAgent {
		if err := sesh.auth.forwardAgent(sesh.connection); err != nil {
			return err
		}
	}

	log.Printf("Initiating session on %s", sesh.Host)
	session, err := sesh.connection.NewSession()
	if err != nil {
		return err
	}

	defer session.Close()

	if cmd.ForwardAgent {
		if err := agent.RequestAgentForwarding(session); err != nil {
			return err
		}
	}

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

	shcmd := cmd.Command
	if dir != "" {
		shcmd = fmt.Sprintf("cd %s; %s", dir, shcmd)
	}

	var cmdErr error
	if cmd.Sudo {
		stdin, err := session.StdinPipe()
		if err != nil {
			return err
		}

		go sesh.writePass(stdin, stdout)
		go io.Copy(&stderrWriter{sesh.Remote}, stderr)

		log.Printf("Invoking cmd on %s", sesh.Host)
		cmdErr = session.Run(fmt.Sprintf("/usr/bin/sudo /bin/bash -c '%s'", shcmd))
	} else {
		go io.Copy(&stdoutWriter{sesh.Remote}, stdout)
		go io.Copy(&stderrWriter{sesh.Remote}, stderr)

		log.Printf("Invoking cmd on %s", sesh.Host)
		cmdErr = session.Run(shcmd)
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

func (sesh *SSHSession) writePass(stdin io.WriteCloser, stdout io.Reader) {
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
			pw, err := sesh.auth.getPassword()
			if err != nil {
				// Welp...
				break
			}

			stdin.Write([]byte(pw))
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

	stdin.Close()
	io.Copy(&stdoutWriter{sesh.Remote}, stdout)
}

func (sesh *SSHSession) mktemp() (string, error) {
	log.Printf("Creating temporary directory on %s", sesh.Host)
	session, err := sesh.connection.NewSession()
	if err != nil {
		return "", err
	}

	defer session.Close()

	result, err := session.CombinedOutput("mktemp -d")
	if err != nil {
		return "", err
	}

	return strings.TrimRight(string(result), "\r\n"), nil
}

func (sesh *SSHSession) deltemp(dir string) error {
	log.Printf("Removing temporary directory on %s", sesh.Host)
	session, err := sesh.connection.NewSession()
	if err != nil {
		return err
	}

	defer session.Close()
	return session.Run("rm -rf " + dir)
}

func (sesh *SSHSession) sendFiles(dir string, files []string) error {
	log.Printf("Preparing to send files to %s", sesh.Host)
	session, err := sesh.connection.NewSession()
	if err != nil {
		return err
	}

	defer session.Close()

	stdin, err := session.StdinPipe()
	if err != nil {
		return err
	}

	result := make(chan error, 1)

	go func() {
		defer stdin.Close()
		for _, file := range files {
			log.Printf("Sending %s to %s", file, sesh.Host)
			f, err := os.Open(file)
			if err != nil {
				log.Printf("Failed to open %s: %s", file, err.Error())
				result <- err
				return
			}

			info, err := f.Stat()
			if err != nil {
				log.Printf("Failed to stat %s: %s", file, err.Error())
				result <- err
				return
			}

			fmt.Fprintf(stdin, "C%04o %d %s\n", info.Mode().Perm(), info.Size(), path.Base(file))
			io.Copy(stdin, f)
			fmt.Fprintf(stdin, "\x00")
		}

		result <- nil
	}()

	out, err := session.CombinedOutput(fmt.Sprintf("/usr/bin/scp -tr %s", dir))
	if err != nil {
		log.Printf("File copy failed on %s [%s] remote: %s", sesh.Host, err.Error(), out)
	}

	sendErr := <-result
	if err == nil {
		err = sendErr
	}

	close(result)

	return err
}
