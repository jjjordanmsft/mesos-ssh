package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
)

type MesosClient struct {
	endpoint string
}

func NewMesosClient(endpoint string) *MesosClient {
	return &MesosClient{
		endpoint: endpoint,
	}
}

func (client *MesosClient) GetAgents() (*MesosAgentsResponse, error) {
	if response, err := client.makeRequest(&MesosRequest{Type: "GET_AGENTS"}); err != nil {
		return nil, err
	} else {
		return response.AgentsResponse, nil
	}
}

func (client *MesosClient) GetVersion() (*MesosVersionResponse, error) {
	if response, err := client.makeRequest(&MesosRequest{Type: "GET_VERSION"}); err != nil {
		return nil, err
	} else {
		return response.VersionResponse, nil
	}
}

func GetMasters() ([]string, error) {
	return net.LookupHost("master.mesos")
}

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

func DiscoverMesos(mesosUri string, msgs *log.Logger) (*MesosClient, error) {
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
