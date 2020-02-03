package contact

import (
    "bufio"
    "fmt"
    "io"
    "net"
    "encoding/json"
    "encoding/hex" // for debugging
    "time"
    "math/rand"
    "strings"
    "../winio"
    "../output"
)

const pipeLetters = "abcdefghijklmnopqrstuvwxyz"
const numPipeLetters := int64(len(pipeLetters))

//PipeAPI communicates through SMB named pipes. Implements the Contact interface
type SmbPipeAPI struct { }

//PipeForwarder forwards data received from SMB pipes to the upstream server. Implements the P2pForwarder interface
type SmbPipeForwarder struct { }

func init() {
	CommunicationChannels["P2pSmbPipe"] = PipeAPI{}
}

// SmbPipeForwarder Implementation

// Listen on agent's main pipe for client connection, and then send them the name of an individual pipe to listen on.
func (forwarder SmbPipeForwarder) ListenForClient(profile map[string]interface{}) {
    pipeName := profile["localPipePath"].(string)

    listener, err := listenPipeFullAccess(pipeName)

    if err != nil {
        output.VerbosePrint(fmt.Sprintf("[!] Error with creating listener for pipe %s", pipeName))
        panic(err)
    }

    defer listener.Close()

    // Whenever a new client connects to pipe with a ping request, generate a new individual pipe for that client, listen on that pipe,
    // and give the pipe name to the client if pipe was successfully created.
    for {
        totalData, err := acceptPipeClientInput(listener)

        if err != nil {
            output.VerbosePrint(fmt.Sprintf("[!] Error with reading client input for pipe %s", pipeName))
            panic(err)
        }

        // convert data to message struct
        var message P2pMessage
        json.Unmarshal(data, &message)

        // Handle request. We only accept pings.
        switch message.InstructionType {
        case INSTR_PING:
            output.VerbosePrint(fmt.Sprintf("[*] Received PING from paw ID %d on behalf of %d", message.CurrentAgentPaw, message.ForwardedForPaw))
            handleSmbPipePing(profile, listener)
        default:
            output.VerbosePrint(fmt.Sprintf("[!] ERROR: expected ping, received %d", message.InstructionType))
        }
    }
}

func (forwarder SmbPipeForwarder) handleSmbPipePing(profile map[string]interface{}, listener net.Listener) {
    // Client pipe names will be 10-15 characters long
    clientPipeNameMinLen = 10
    clientPipeNameMaxLen = 15

    output.VerbosePrint("[*] Waiting for client to connect before sending ping response")
    conn, err := listener.Accept()

    defer conn.Close()

    if err != nil {
        output.VerbosePrint("[!] Error with accepting connection to listener.")
        return
    }

    output.VerbosePrint("[*] Connection received from client")

    // Create random pipe name
    rand.Seed(time.Now().UnixNano())
    clientPipeName = getRandPipeName(rand.Intn(clientPipeNameMaxLen - clientPipeNameMinLen) + clientPipeNameMinLen)
    output.VerbosePrint(fmt.Sprintf("[*] Making individual client pipe %s", clientPipeName))

    // Start forwarder on client pipe and send name to client.
    go forwarder.StartForwarder(profile, clientPipeName)

    pipeWriter := bufio.NewWriter(conn)

    // Write & flush data and close connection.
    writePipeData([]bytes(clientPipeName), pipeWriter)
    conn.Close()
}

// Sets up listener on pipe for individual client
func (forwarder SmbPipeForwarder) StartForwarder(profile map[string]interface{}, pipeName string) {
    listener, err := listenPipeFullAccess(pipeName)

    if err != nil {
        output.VerbosePrint(fmt.Sprintf("[!] Error with creating listener for pipe %s", pipeName))
        panic(err)
    }

    defer listener.Close()

	for {
        // Get data from client
        totalData, err := acceptPipeClientInput(listener)

        if err != nil {
            output.VerbosePrint(fmt.Sprintf("[!] Error with reading client input for pipe %s", pipeName))
            panic(err)
        }

        // Handle data
        listenerHandlePipePayload(totalData, profile, listener)
	}
}

// Helper function that listens on pipe and returns listener and any error.
func (forwarder SmbPipeForwarder) listenPipeFullAccess(pipeName string) (net.Listener, error) {
    config := &winio.PipeConfig{
        SecurityDescriptor: "D:(A;;GA;;;S-1-1-0)", // File all access to everyone.
    }
    return winio.ListenPipe(pipeName, config)
}

// Helper function that creates random string of specified length using letters a-z
func getRandPipeName(length int) string {
    rand.Seed(time.Now().UnixNano())
    buffer := make([]byte, length)
    for i := range buffer {
        buffer[i] = pipeLetters[rand.Int63() % numPipeLetters]
    }

    return string(buffer)
}

// Helper function that waits for client to connect to the listener and returns data sent by client.
func acceptPipeClientInput(listener net.Listener) ([]byte, error) {
    output.VerbosePrint("[*] Waiting for connection from client to receive input")
    conn, err := listener.Accept()

    defer conn.Close()

    if err != nil {
        output.VerbosePrint("[!] Error with accepting connection to listener.")
        return nil, err
    }
    output.VerbosePrint("[*] Connection received from client")

    pipeReader := bufio.NewReader(conn)

    // Read in the data and close connection.
    data, _ := readPipeData(pipeReader)
    return data, nil
}

// Helper function that handles data received from the named pipe by sending it to the agent's c2/upstream server.
// Waits for original client to connect to listener before writing response back. TODO set timeout.
// Does not handle pings - pings are handled in ListenForClient.
func listenerHandlePipePayload(data []byte, profile map[string]interface{}, listener net.Listener) {
    upstreamDest := profile["server"].(string)
    upstreamComms, _ := CommunicationChannels[profile["c2"].(string)]

    // Placeholder debugging
    output.VerbosePrint(fmt.Sprintf("[*] Received data (hex): %s", hex.EncodeToString(data)))
    output.VerbosePrint(fmt.Sprintf("[*] Forwarding message upstream to %s via %s", upstreamDest, profile["c2"].(string)))

    // convert data to message struct
    var message P2pMessage
    json.Unmarshal(data, &message)

    switch message.InstructionType {
    case INSTR_PING:
        output.VerbosePrint("[!] WARNING: unexpected ping.")
	case INSTR_GET_INSTRUCTIONS:
	    forwardGetInstructions(data, profile, listener, upstreamComms)
	case INSTR_DROP_PAYLOAD:
	    forwardDropPayload(data, profile, listener, upstreamComms)
	case INSTR_SEND_EXECUTION_RESULTS:
	    forwardSendExecResults(data, profile, listener, upstreamComms)
    default:
        output.VerbosePrint(fmt.Sprintf("[!] ERROR: invalid instruction type for p2p message %d", message.InstructionType))
    }

    /*
    if upstreamProtocol == "HTTP" {
        // Hardcoded as instructions for now - TODO add flexibility
        address := fmt.Sprintf("%s/instructions", upstreamDest)
        bites := postRequest(address, data)

        if bites != nil {
            output.VerbosePrint("[*] Waiting for client to connect before sending response")
            conn, err := listener.Accept()

            defer conn.Close()

            if err != nil {
                output.VerbosePrint("[!] Error with accepting connection to listener.")
                panic(err)
            }

            output.VerbosePrint("[*] Connection received from client")
            output.VerbosePrint("[+] relaying beacon: ALIVE")

            pipeWriter := bufio.NewWriter(conn)

            // Write & flush data and close connection.
            writePipeData(bites, pipeWriter)
            conn.Close()
        } else {
            output.VerbosePrint("[-] beacon for relay: DEAD")
        }
    }
    */
}

func forwardGetInstructions(data []byte, profile map[string]interface{}, listener net.Listener, upstreamComms Contact) {

}

func forwardDropPayload(data []byte, profile map[string]interface{}, listener net.Listener, upstreamComms Contact) {

}

func forwardSendExecResults(data []byte, profile map[string]interface{}, listener net.Listener, upstreamComms Contact) {

}



// SmbPipeAPI implementation

// Dial to pipe using server and get individual pipe name to connect to for other transactions.
func (p2pPipeClient SmbPipeAPI) Ping(profile map[string]interface{}) bool {
    // Build SMB Pipe message for ping.
    pipeMsg := make(map[string]interface{})
    pipeMsg["current_agent"] = int64(profile["paw"])
    pipeMsg["forwarded_for"] = int64(profile["paw"])
    pipeMsg["instruction_type"] = INSTR_PING
    pipeMsg["payload"] = nil
    pipeMsgData, _ := json.Marshal(pipeMsg)

    // Send beacon and fetch response
    pipePath := profile["server"].(string)
    sendSmbPipeClientInput(pipePath, pipeMsgData)
    responseData := fetchForwarderResponse(pipePath)

    if responseData != nil {
        // We got the pipe name to use next.
        newPipeName := string(responseData)
        output.VerbosePrint(fmt.Sprintf("[*] Obtained individual pipe name %s", newPipeName))

        // Replace server for agent.
        serverHostname := strings.Split(profile["server"].(string), "\\")[2]
        newServerPipePath := "\\\\" + serverHostName + "\\" + newPipeName
        output.VerbosePrint(fmt.Sprintf("[*] Setting new server %s", newServerPipePath))

        profile["server"] = newServerPipePath

        return true
    } else {
        return false
    }
}

// Sends data to specified pipe.
func sendSmbPipeClientInput(pipePath string, data []byte) {
    conn, err := winio.DialPipe(pipePath, nil)

    if err != nil {
        output.VerbosePrint(fmt.Sprintf("[!] Error dialing to pipe %s", pipePath))
        panic(err)
    }

    // Write data and close connection.
    writer := bufio.NewWriter(conn)
    writePipeData(data, writer)

    conn.Close()
}

// Read response data from forwarder using given pipePath.
func fetchForwarderResponse(pipePath string) []byte {
    conn, err := winio.DialPipe(pipePath, nil)

    if err != nil {
        output.VerbosePrint(fmt.Sprintf("[!] Error dialing to pipe %s", pipePath))
        return nil
    }

    defer conn.Close()

    output.VerbosePrint("[*] Fetching response data from forwarder.")

    // Read data and return.
    pipeReader := bufio.NewReader(conn)
    data, _ := readPipeData(pipeReader)
    return data
}

/*
 * Other auxiliary functions
 */

// Returns data and number of bytes read.
func readPipeData(pipeReader *bufio.Reader) ([]byte, int64) {
    buffer := make([]byte, 4*1024)
    totalData := make([]byte, 0)
    numBytes := int64(0)
    numChunks := int64(0)

    output.VerbosePrint("[*] Going to read data from pipe")

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
        if err == io.ErrClosedPipe {
	        output.VerbosePrint("[!] Pipe closed. Not able to flush data.")
	        return
	    } else {
	        output.VerbosePrint("[!] Error writing data to pipe")
            panic(err)
	    }
    }

    err = pipeWriter.Flush()
	if err != nil {
	    if err == io.ErrClosedPipe {
	        output.VerbosePrint("[!] Pipe closed. Not able to flush data.")
	        return
	    } else {
	        output.VerbosePrint("[!] Error flushing data to pipe")
		    panic(err)
	    }
	}

    output.VerbosePrint(fmt.Sprintf("[*] Wrote %d bytes to pipe", n))
}