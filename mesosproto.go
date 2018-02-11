package main

import "time"

// Serialization format for mesos HTTP API protocol

type MesosRequest struct {
	Type           string          `json:"type"`
	MetricsTimeout *MesosTimestamp `json:"get_metrics,omitempty"`
}

type MesosResponse struct {
	Type            string                `json:"type"`
	AgentsResponse  *MesosAgentsResponse  `json:"get_agents"`
	VersionResponse *MesosVersionResponse `json:"get_version"`
}

type MesosVersionResponse struct {
	VersionInfo struct {
		Version   string  `json:"version"`
		BuildDate string  `json:"build_date"`
		BuildTime float64 `json:"build_time"`
		BuildUser string  `json:"build_user"`
	} `json:"version_info"`
}

type MesosAgentsResponse struct {
	Agents []*MesosAgent `json:"agents"`
}

type MesosAgent struct {
	Active             bool             `json:"active"`
	AgentInfo          MesosAgentInfo   `json:"agent_info"`
	AllocatedResources []*MesosResource `json:"allocated_resources"`
	Pid                string           `json:"pid"`
	RegisteredTime     MesosTimestamp   `json:"registered_time"`
	TotalResources     []*MesosResource `json:"total_resources"`
}

type MesosAgentInfo struct {
	Hostname  string           `json:"hostname"`
	Id        MesosTextValue   `json:"id"`
	Port      int              `json:"port"`
	Resources []*MesosResource `json:"resources"`
}

type MesosTextValue struct {
	Value *string `json:"value"`
}

type MesosTimestamp struct {
	Nanoseconds int64 `json:"nanoseconds"`
}

type MesosResource struct {
	Name   string         `json:"name"`
	Role   string         `json:"role,omitempty"`
	Type   string         `json:"type"`
	Text   MesosTextValue `json:"text"`
	Scalar struct {
		Value float64 `json:"value"`
	} `json:"scalar"`
	Ranges struct {
		Range []struct {
			Begin int `json:"begin"`
			End   int `json:"end"`
		} `json:"range"`
	} `json:"ranges"`
}

func (text *MesosTextValue) Empty() bool {
	return text.Value == nil
}

func (text *MesosTextValue) String() string {
	if !text.Empty() {
		return *text.Value
	}
	return ""
}

func (timestamp *MesosTimestamp) Time() time.Time {
	return time.Unix(0, timestamp.Nanoseconds)
}
