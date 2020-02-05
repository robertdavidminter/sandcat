package contact

import (
    "encoding/json"
)

const (
	INSTR_GET_INDIVID_PIPE = 0
	INSTR_GET_INSTRUCTIONS = 1
	INSTR_GET_PAYLOAD_BYTES = 2
	INSTR_SEND_EXECUTION_RESULTS = 3
)

//P2pReceiver defines required functions for relaying messages between peers and an upstream peer/c2.
type P2pReceiver interface {
	StartReceiver(profile map[string]interface{}, p2pReceiverConfig map[string]string, upstreamComs Contact)
}


// Defines message structure for p2p
type P2pMessage struct {
    RequestingAgentPaw string
    InstructionType int64
    Payload []byte
}

// Helper Functions

// Build p2p message and return the bytes of its JSON marshal.
func buildP2pMsgBytes(paw string, instruction int, payload []byte) []byte {
    p2pMsg := make(map[string]interface{})
    p2pMsg["RequestingAgentPaw"] = paw
    p2pMsg["InstructionType"] = instruction
    p2pMsg["Payload"] = payload
    p2pMsgData, _ := json.Marshal(p2pMsg)

    return p2pMsgData
}

// Convert bytes of JSON marshal into P2pMessage struct
func bytesToP2pMsg(data []byte) P2pMessage {
    var message P2pMessage
    json.Unmarshal(data, &message)

    return message
}

// P2pReceiverChannels contains the P2pReceiver implementations
var P2pReceiverChannels = map[string]P2pReceiver{}