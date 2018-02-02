package main

import (
	"fmt"
	"time"
)

type IOCollector interface {
	NewRemote(host string) RemoteIO
	Read()
}

type RemoteIO interface {
	Stdout(data []byte)
	Stderr(data []byte)
	Exit(status int)
	Done(err error)
}

type stdoutWriter struct {
	remote RemoteIO
}

func (writer *stdoutWriter) Write(p []byte) (int, error) {
	writer.remote.Stdout(p)
	return len(p), nil
}

type stderrWriter struct {
	remote RemoteIO
}

func (writer *stderrWriter) Write(p []byte) (int, error) {
	writer.remote.Stderr(p)
	return len(p), nil
}

type RegularIOCollector struct {
	results chan *IOResult
	count   int
}

type IOMessage struct {
	data   string
	stream int
}

type IOResult struct {
	host   string
	msgs   []*IOMessage
	result error
}

type RegularRemoteIO struct {
	host       string
	collector  chan *IOMessage
	controller chan *IOResult
	done       chan error
}

func NewRegularIOCollector() IOCollector {
	collector := &RegularIOCollector{
		results: make(chan *IOResult),
	}

	return collector
}

func (coll *RegularIOCollector) NewRemote(host string) RemoteIO {
	remote := &RegularRemoteIO{
		host:       host,
		collector:  make(chan *IOMessage),
		done:       make(chan error),
		controller: coll.results,
	}

	coll.count++
	go remote.process()

	return remote
}

func (coll *RegularIOCollector) Read() {
	recvd := 0

	for recvd < coll.count {
		result := <-coll.results
		fmt.Printf("\n===== Results from %s\n", result.host)
		for _, x := range result.msgs {
			fmt.Printf("%s", x.data)
		}
		if result.result != nil {
			fmt.Printf("==> Failed with %s\n", result.result.Error())
		}
		recvd++
	}
}

func (remote *RegularRemoteIO) process() {
	var msgs []*IOMessage
	var result error
wait:
	for {
		select {
		case msg := <-remote.collector:
			msgs = append(msgs, msg)
		case err := <-remote.done:
			result = err
			break wait
		}
	}

	// Give the data streams up to 250ms to finish sending.
	deadline := 250 * time.Millisecond
	t := time.NewTimer(deadline)
wait2:
	for {
		select {
		case msg := <-remote.collector:
			msgs = append(msgs, msg)

			if !t.Stop() {
				<-t.C
			}
			t.Reset(deadline)
		case <-t.C:
			break wait2
		}
	}

	remote.controller <- &IOResult{
		msgs:   msgs,
		host:   remote.host,
		result: result,
	}
}

func (remote *RegularRemoteIO) Stdout(data []byte) {
	remote.collector <- &IOMessage{
		data:   string(data),
		stream: 1,
	}
}

func (remote *RegularRemoteIO) Stderr(data []byte) {
	remote.collector <- &IOMessage{
		data:   string(data),
		stream: 2,
	}
}

func (remote *RegularRemoteIO) Exit(code int) {
	remote.collector <- &IOMessage{
		data:   fmt.Sprintf("Exited with code: %d\n", code),
		stream: -1,
	}
}

func (remote *RegularRemoteIO) Done(err error) {
	remote.done <- err
}
