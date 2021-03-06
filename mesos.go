package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"strings"
)

// Lookup hosts for "spec" from mesos leader "mesos". Write any output to msgs.
func GetHosts(mesos, spec string, msgs *log.Logger) ([]string, error) {
	if spec == "masters" {
		return getMasters()
	}

	if spec == "agents" || spec == "all" || spec == "public" || spec == "private" {
		var result []string
		mesosClient, err := discoverMesos(mesos, msgs)
		if err != nil {
			return result, err
		}

		agents, err := mesosClient.GetAgents()
		if err != nil {
			return result, err
		}

		if spec == "agents" || spec == "all" {
			result, err = filterAgents(agents, func(ag *MesosAgent) bool { return true }), nil
			if err != nil {
				return result, err
			}

			if spec == "all" {
				masters, err := getMasters()
				if err != nil {
					return result, err
				}

				result = append(result, masters...)
			}

			return result, nil
		} else if spec == "public" {
			return filterAgents(agents, hasPublicResource), nil
		} else if spec == "private" {
			return filterAgents(agents, func(ag *MesosAgent) bool { return !hasPublicResource(ag) }), nil
		}

		return result, fmt.Errorf("Should not be reachable")
	} else {
		var result []string

		contents, err := ioutil.ReadFile(spec)
		if err != nil {
			return result, err
		}

		lines := strings.Split(string(contents), "\n")
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if len(trimmed) > 0 {
				result = append(result, trimmed)
			}
		}

		return result, nil
	}
}

// Pared-down mesos client.
type MesosClient struct {
	endpoint string
}

func NewMesosClient(endpoint string) *MesosClient {
	return &MesosClient{
		endpoint: endpoint,
	}
}

// Get all agents
func (client *MesosClient) GetAgents() (*MesosAgentsResponse, error) {
	if response, err := client.makeRequest(&MesosRequest{Type: "GET_AGENTS"}); err != nil {
		return nil, err
	} else {
		return response.AgentsResponse, nil
	}
}

// Get version. Used to check for a Mesos endpoint.
func (client *MesosClient) GetVersion() (*MesosVersionResponse, error) {
	if response, err := client.makeRequest(&MesosRequest{Type: "GET_VERSION"}); err != nil {
		return nil, err
	} else {
		return response.VersionResponse, nil
	}
}

// Lookup mesos masters
func getMasters() ([]string, error) {
	return net.LookupHost("master.mesos")
}

// Make a request to Mesos
func (client *MesosClient) makeRequest(request *MesosRequest) (*MesosResponse, error) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(request); err != nil {
		return nil, err
	}

	httpClient := &http.Client{}

	req, err := http.NewRequest("POST", client.endpoint+"/api/v1", &buf)
	if err != nil {
		return nil, err
	}

	req.Header.Add("Content-type", "application/json")
	resp, err := httpClient.Do(req)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()
	result := &MesosResponse{}
	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return nil, err
	}

	if result.Type != request.Type {
		return nil, fmt.Errorf("Unexpected response type '%s', wanted '%s'", result.Type, request.Type)
	}

	return result, nil
}

// Find Mesos leader
func discoverMesos(mesosUri string, msgs *log.Logger) (*MesosClient, error) {
	if mesosUri != "" {
		client := NewMesosClient(mesosUri)
		_, err := client.GetVersion()
		if err == nil {
			// This works- take the client-supplied endpoint
			return client, nil
		}

		msgs.Println("Failed to connect to Mesos with client-supplied path, trying autodiscovery.")
	}

	if _, addrs, err := net.LookupSRV("leader", "tcp", "mesos"); err == nil && len(addrs) > 0 {
		for _, addr := range addrs {
			uri := fmt.Sprintf("http://%s:%s", addr.Target, addr.Port)
			client := NewMesosClient(uri)
			_, err := client.GetVersion()
			if err == nil {
				return client, nil
			}
		}
	} else {
		msgs.Printf("Failed to lookup leader.mesos SRV record: %s", err.Error())
	}

	// Try http://leader.mesos:5050
	client := NewMesosClient("http://leader.mesos:5050")
	if _, err := client.GetVersion(); err == nil {
		return client, nil
	} else {
		return nil, fmt.Errorf("Failed checking leader.mesos:5050: %s", err.Error())
	}
}

// Find hosts of agents that match a predicate
func filterAgents(resp *MesosAgentsResponse, f func(agent *MesosAgent) bool) []string {
	var result []string
	for _, agent := range resp.Agents {
		if f(agent) {
			result = append(result, agent.AgentInfo.Hostname)
		}
	}

	return result
}

// Distinguish between "public" and "private" agents.
func hasPublicResource(agent *MesosAgent) bool {
	for _, resource := range agent.AgentInfo.Resources {
		if resource.Role == "slave_public" {
			return true
		}
	}

	return false
}
