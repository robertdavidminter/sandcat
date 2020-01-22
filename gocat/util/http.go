package util

import (
    "net/http"
    "io/ioutil"
    "bytes"
)

func postRequest(address string, data []byte) []byte {
	req, _ := http.NewRequest("POST", address, bytes.NewBuffer(Encode(data)))
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	body, _ := ioutil.ReadAll(resp.Body)
	return Decode(string(body))
}