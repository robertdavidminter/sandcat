package contact

const (
	INSTR_PING = 0
	INSTR_GET_INSTRUCTIONS = 1
	INSTR_DROP_PAYLOAD = 2
	INSTR_SEND_EXECUTION_RESULTS = 3
)

//P2pForwarder defines required functions for communicating with the server
type P2pForwarder interface {
	StartForwarder(profile map[string]interface{}, pipeName string, upstreamComs Contact)
}


// Defines message structure for p2p
type P2pMessage struct {
    RequestingAgentPaw string
    InstructionType int64
    Payload []byte
}