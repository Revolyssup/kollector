package watch

import (
	"encoding/json"
	"log"

	"k8s.io/apimachinery/pkg/version"
)

type JsonType int
type StateType int

const (
	NODE          JsonType = 1
	SERVICES      JsonType = 2
	MICROSERVICES JsonType = 3
	PODS          JsonType = 4
	SECRETS       JsonType = 5
)

const (
	CREATED StateType = 1
	DELETED StateType = 2
	UPDATED StateType = 3
)

type ObjectData struct {
	Created []interface{} `json:"create,omitempty"`
	Deleted []interface{} `json:"delete,omitempty"`
	Updated []interface{} `json:"update,omitempty"`
}

type jsonFormat struct {
	FirstReport             bool          `json:"firstReport"`
	ClusterAPIServerVersion *version.Info `json:"clusterAPIServerVersion,omitempty"`
	CloudVendor             string        `json:"cloudVendor,omitempty"`
	Nodes                   *ObjectData   `json:"node,omitempty"`
	Services                *ObjectData   `json:"service,omitempty"`
	MicroServices           *ObjectData   `json:"microservice,omitempty"`
	Pods                    *ObjectData   `json:"pod,omitempty"`
	Secret                  *ObjectData   `json:"secret,omitempty"`
}

func (obj *ObjectData) AddToJsonFormatByState(NewData interface{}, stype StateType) {
	switch stype {
	case CREATED:
		obj.Created = append(obj.Created, NewData)
	case DELETED:
		obj.Deleted = append(obj.Deleted, NewData)
	case UPDATED:
		obj.Updated = append(obj.Updated, NewData)
	}
}

func (obj *ObjectData) Len() int {
	sum := 0
	if obj == nil {
		return sum
	}
	if obj.Created != nil {
		sum += len(obj.Created)
	}
	if obj.Deleted != nil {
		sum += len(obj.Deleted)
	}
	if obj.Updated != nil {
		sum += len(obj.Updated)
	}
	return sum
}

func (jsonReport *jsonFormat) AddToJsonFormat(data interface{}, jtype JsonType, stype StateType) {
	switch jtype {
	case NODE:
		if jsonReport.Nodes == nil {
			jsonReport.Nodes = &ObjectData{}
		}
		jsonReport.Nodes.AddToJsonFormatByState(data, stype)
	case SERVICES:
		if jsonReport.Services == nil {
			jsonReport.Services = &ObjectData{}
		}
		jsonReport.Services.AddToJsonFormatByState(data, stype)
	case MICROSERVICES:
		if jsonReport.MicroServices == nil {
			jsonReport.MicroServices = &ObjectData{}
		}
		jsonReport.MicroServices.AddToJsonFormatByState(data, stype)
	case PODS:
		if jsonReport.Pods == nil {
			jsonReport.Pods = &ObjectData{}
		}
		jsonReport.Pods.AddToJsonFormatByState(data, stype)
	case SECRETS:
		if jsonReport.Secret == nil {
			jsonReport.Secret = &ObjectData{}
		}
		jsonReport.Secret.AddToJsonFormatByState(data, stype)
	}

}

//PrepareDataToSend -
func PrepareDataToSend(wh *WatchHandler) []byte {
	jsonReport := wh.jsonReport
	if *wh.GetAggregateFirstDataFlag() {
		jsonReport.ClusterAPIServerVersion = wh.clusterAPIServerVersion
		jsonReport.CloudVendor = wh.cloudVendor
	} else {
		jsonReport.ClusterAPIServerVersion = nil
		jsonReport.CloudVendor = ""
	}
	if jsonReport.Nodes.Len() == 0 {
		jsonReport.Nodes = nil
	}
	if jsonReport.Services.Len() == 0 {
		jsonReport.Services = nil
	}
	if jsonReport.Secret.Len() == 0 {
		jsonReport.Secret = nil
	}
	if jsonReport.Pods.Len() == 0 {
		jsonReport.Pods = nil
	}
	if jsonReport.MicroServices.Len() == 0 {
		jsonReport.MicroServices = nil
	}
	jsonReportToSend, err := json.Marshal(jsonReport)
	if nil != err {
		log.Printf("json.Marshal %v", err)
		return nil
	}
	deleteJsonData(wh)
	wh.aggregateFirstDataFlag = false
	return jsonReportToSend
}

//WaitTillNewDataArrived -
func WaitTillNewDataArrived(wh *WatchHandler) bool {
	<-wh.informNewDataChannel
	return true
}

func informNewDataArrive(wh *WatchHandler) {
	if !wh.aggregateFirstDataFlag {
		wh.informNewDataChannel <- 1
	}
}

func deleteObjecData(l *[]interface{}) {
	*l = []interface{}{}
}

func deleteJsonData(wh *WatchHandler) {
	jsonReport := &wh.jsonReport

	if jsonReport.Nodes != nil {
		deleteObjecData(&jsonReport.Nodes.Created)
		deleteObjecData(&jsonReport.Nodes.Deleted)
		deleteObjecData(&jsonReport.Nodes.Updated)
	}

	if jsonReport.Pods != nil {
		deleteObjecData(&jsonReport.Pods.Created)
		deleteObjecData(&jsonReport.Pods.Deleted)
		deleteObjecData(&jsonReport.Pods.Updated)
	}

	if jsonReport.Services != nil {
		deleteObjecData(&jsonReport.Services.Created)
		deleteObjecData(&jsonReport.Services.Deleted)
		deleteObjecData(&jsonReport.Services.Updated)
	}

	if jsonReport.MicroServices != nil {
		deleteObjecData(&jsonReport.MicroServices.Created)
		deleteObjecData(&jsonReport.MicroServices.Deleted)
		deleteObjecData(&jsonReport.MicroServices.Updated)
	}

	if jsonReport.Secret != nil {
		deleteObjecData(&jsonReport.Secret.Created)
		deleteObjecData(&jsonReport.Secret.Deleted)
		deleteObjecData(&jsonReport.Secret.Updated)
	}
}
