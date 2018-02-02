package main

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"time"
)

type IOCollector interface {
	NewRemote(host string) *RemoteIO
	Read()
}

type IOMessage struct {
	data   string
	stream int
}

type RemoteIO struct {
	host      string
	collector chan *IOMessage
	done      chan error
}

func NewRemoteIO(host string) *RemoteIO {
	return &RemoteIO{
		host:      host,
		collector: make(chan *IOMessage),
		done:      make(chan error),
	}
}

func (remote *RemoteIO) Stdout(data []byte) {
	remote.collector <- &IOMessage{
		data:   string(data),
		stream: 1,
	}
}

func (remote *RemoteIO) Stderr(data []byte) {
	remote.collector <- &IOMessage{
		data:   string(data),
		stream: 2,
	}
}

func (remote *RemoteIO) Exit(code int) {
	remote.collector <- &IOMessage{
		data:   fmt.Sprintf("Exited with code: %d\n", code),
		stream: -1,
	}
}

func (remote *RemoteIO) Done(err error) {
	remote.done <- err
}

type stdoutWriter struct {
	remote *RemoteIO
}

func (writer *stdoutWriter) Write(p []byte) (int, error) {
	writer.remote.Stdout(p)
	return len(p), nil
}

type stderrWriter struct {
	remote *RemoteIO
}

func (writer *stderrWriter) Write(p []byte) (int, error) {
	writer.remote.Stderr(p)
	return len(p), nil
}

type RegularIOCollector struct {
	results chan *IOResult
	count   int
}

type IOResult struct {
	host   string
	msgs   []*IOMessage
	result error
}

func NewRegularIOCollector() IOCollector {
	return &RegularIOCollector{
		results: make(chan *IOResult),
	}
}

func (coll *RegularIOCollector) NewRemote(host string) *RemoteIO {
	remote := NewRemoteIO(host)
	coll.count++
	go coll.process(remote)
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

func (coll *RegularIOCollector) process(remote *RemoteIO) {
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

	coll.results <- &IOResult{
		msgs:   msgs,
		host:   remote.host,
		result: result,
	}
}

type InterleavedIOCollector struct {
	messages  chan *IOMessage
	waitgroup sync.WaitGroup
}

func NewInterleavedIOCollector() IOCollector {
	return &InterleavedIOCollector{
		messages: make(chan *IOMessage),
	}
}

func (coll *InterleavedIOCollector) NewRemote(host string) *RemoteIO {
	remote := NewRemoteIO(host)
	coll.waitgroup.Add(1)
	go coll.process(remote)
	return remote
}

func (coll *InterleavedIOCollector) Read() {
	done := make(chan bool)
	go func() {
		coll.waitgroup.Wait()
		done <- true
	}()

	for {
		select {
		case msg := <-coll.messages:
			fmt.Println(msg.data)
		case <-done:
			return
		}
	}
}

func (coll *InterleavedIOCollector) process(remote *RemoteIO) {
	defer coll.waitgroup.Done()
	processor := &interleavedProcessor{
		collector: coll,
		remote:    remote,
		curStream: -1,
	}
	processor.process()
}

type interleavedProcessor struct {
	collector *InterleavedIOCollector
	remote    *RemoteIO
	curStream int
	buf       bytes.Buffer
}

func (proc *interleavedProcessor) process() {
	var result error
wait:
	for {
		select {
		case msg := <-proc.remote.collector:
			proc.handle(msg)
		case err := <-proc.remote.done:
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
		case msg := <-proc.remote.collector:
			proc.handle(msg)

			if !t.Stop() {
				<-t.C
			}
			t.Reset(deadline)
		case <-t.C:
			break wait2
		}
	}

	if result != nil {
		proc.handle(&IOMessage{
			data:   fmt.Sprintf("Failed with %s\n", result.Error()),
			stream: -1,
		})
	}

	proc.flush()
}

func (proc *interleavedProcessor) handle(msg *IOMessage) {
	if msg.stream != proc.curStream {
		proc.flush()
		proc.curStream = msg.stream
	}

	data := msg.data
	for {
		nl := strings.IndexByte(data, '\n')
		if nl < 0 {
			break
		}

		if proc.buf.Len() > 0 {
			proc.buf.WriteString(data[:nl])
			proc.flush()
		} else {
			proc.send(data[:nl])
		}

		data = data[nl+1:]
	}

	proc.buf.WriteString(data)
}

func (proc *interleavedProcessor) flush() {
	if proc.buf.Len() > 0 {
		proc.send(proc.buf.String())
		proc.buf.Reset()
	}
}

func (proc *interleavedProcessor) send(line string) {
	var stream string
	switch proc.curStream {
	case 1:
		stream = "out"
	case 2:
		stream = "err"
	case -1:
		stream = "***"
	default:
		stream = fmt.Sprintf("%03d", proc.curStream)
	}

	proc.collector.messages <- &IOMessage{
		data:   fmt.Sprintf("%s [%s]: %s", proc.remote.host, stream, line),
		stream: proc.curStream,
	}
}
