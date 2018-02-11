package main

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Time to wait for remaining IO after process exit.
const deadline time.Duration = 100 * time.Millisecond

// Top-level IO collector for SSH output
type IOCollector interface {
	NewRemote(host string) *RemoteIO
	Read()
}

// A single packet of output
type IOMessage struct {
	data   string
	stream int
}

// Exists per host and sends IO to be aggregated back to IOCollector
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

// Send data to stdout
func (remote *RemoteIO) Stdout(data []byte) {
	remote.collector <- &IOMessage{
		data:   string(data),
		stream: 1,
	}
}

// Send data to stderr
func (remote *RemoteIO) Stderr(data []byte) {
	remote.collector <- &IOMessage{
		data:   string(data),
		stream: 2,
	}
}

// Indicates an exit with return code
func (remote *RemoteIO) Exit(code int) {
	remote.collector <- &IOMessage{
		data:   fmt.Sprintf("Exited with code: %d\n", code),
		stream: -1,
	}
}

// Indicates the client has terminated
func (remote *RemoteIO) Done(err error) {
	remote.done <- err
}

// io.Writer to stdout for the specified RemoteIO
type stdoutWriter struct {
	remote *RemoteIO
}

func (writer *stdoutWriter) Write(p []byte) (int, error) {
	writer.remote.Stdout(p)
	return len(p), nil
}

// io.Writer to stderr for the specified RemoteIO
type stderrWriter struct {
	remote *RemoteIO
}

func (writer *stderrWriter) Write(p []byte) (int, error) {
	writer.remote.Stderr(p)
	return len(p), nil
}

// IOCollector that displays outputs one-at-a-time after each connection closes.
type RegularIOCollector struct {
	results chan *IOResult
	count   int
}

// Full output from a remote connection
type IOResult struct {
	host   string
	msgs   []*IOMessage
	result error
}

// Makes a RegularIOCollector
func NewRegularIOCollector() IOCollector {
	return &RegularIOCollector{
		results: make(chan *IOResult),
	}
}

// Creates a new RemoteIO for the specified host
func (coll *RegularIOCollector) NewRemote(host string) *RemoteIO {
	remote := NewRemoteIO(host)
	coll.count++
	go coll.process(remote)
	return remote
}

// Collects and displays output from all RemoteIO's, then returns
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

	close(coll.results)
}

// Reads output from a single RemoteIO, sends it all back to collector when
// it is finished.
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

	// Give the data streams some time to finish sending.
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

	close(remote.collector)
	close(remote.done)
}

// IOCollector that interleaves output from many remote hosts as it arrives.
type InterleavedIOCollector struct {
	messages  chan *IOMessage
	waitgroup sync.WaitGroup
}

// Creates an InterleavedIOCollector
func NewInterleavedIOCollector() IOCollector {
	return &InterleavedIOCollector{
		messages: make(chan *IOMessage),
	}
}

// Creates a RemoteIO that feeds the InterleavedIOCollector for the specified host.
func (coll *InterleavedIOCollector) NewRemote(host string) *RemoteIO {
	remote := NewRemoteIO(host)
	coll.waitgroup.Add(1)
	go coll.process(remote)
	return remote
}

// Collects and displays output from all RemoteIO's, then returns
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
			close(coll.messages)
			close(done)
			return
		}
	}
}

// Reads output from a single RemoteIO and forwards it to the InterleavedIOCollector
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

	// Give the data streams some time to finish sending.
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
	close(proc.remote.collector)
	close(proc.remote.done)
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
