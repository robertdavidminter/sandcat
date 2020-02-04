package contact

const (
	ok = 200
	created = 201
)

//Contact defines required functions for communicating with the server
type Contact interface {
	Ping(profile map[string]interface{}) bool
	GetInstructions(profile map[string]interface{}) map[string]interface{}
	DropPayloads(profile map[string]interface{}, payload string) []string
	RunInstruction(command map[string]interface{}, profile map[string]interface{}, payloads []string)
	C2RequirementsMet(profile map[string]interface{}, criteria interface{}) bool
	GetPayloadBytes(payload string, server string, uniqueID string, platform string) []byte
	SendExecutionResults(commandID interface{}, server interface{}, result []byte, status string, cmd string, pid string, uniqueID string)
}

//CommunicationChannels contains the contact implementations
var CommunicationChannels = map[string]Contact{}