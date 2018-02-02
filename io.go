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
	OnStdout(data []byte)
	OnStderr(data []byte)
	OnExit(status int)
}

type stdoutWriter struct {
	remote RemoteIO
}

func (writer *stdoutWriter) Write(p []byte) (int, error) {
	writer.remote.OnStdout(p)
	return len(p), nil
}

type stderrWriter struct {
	remote RemoteIO
}

func (writer *stderrWriter) Write(p []byte) (int, error) {
	writer.remote.OnStderr(p)
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
	result int
}

type RegularRemoteIO struct {
	host       string
	collector  chan *IOMessage
	exit       chan int
	controller chan *IOResult
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
		exit:       make(chan int),
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
		fmt.Printf("==> Result was: %d\n", result.result)
		recvd++
	}
}

func (remote *RegularRemoteIO) process() {
	var msgs []*IOMessage
	var result int
wait:
	for {
		select {
		case msg := <-remote.collector:
			msgs = append(msgs, msg)
		case code := <-remote.exit:
			result = code
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

func (remote *RegularRemoteIO) OnStdout(data []byte) {
	remote.collector <- &IOMessage{
		data:   string(data),
		stream: 1,
	}
}

func (remote *RegularRemoteIO) OnStderr(data []byte) {
	remote.collector <- &IOMessage{
		data:   string(data),
		stream: 2,
	}
}

func (remote *RegularRemoteIO) OnExit(code int) {
	remote.exit <- code
}
