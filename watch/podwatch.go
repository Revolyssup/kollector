package watch

import (
	"container/list"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	opapoliciesstore "k8s-ca-dashboard-aggregator/opapolicies"

	"github.com/golang/glog"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/api/batch/v1beta1"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/watch"
)

type OwnerDet struct {
	Name      string      `json:"name"`
	Kind      string      `json:"kind"`
	OwnerData interface{} `json:"ownerData,omitempty"`
}
type CRDOwnerData struct {
	v1.TypeMeta
}
type OwnerDetNameAndKindOnly struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type MicroServiceData struct {
	*core.Pod `json:",inline"`
	Owner     OwnerDet `json:"uptreeOwner"`
	PodSpecId int      `json:"podSpecId"`
}

type PodDataForExistMicroService struct {
	PodName           string                  `json:"podName"`
	NodeName          string                  `json:"nodeName"`
	PodIP             string                  `json:"podIP"`
	Namespace         string                  `json:"namespace,omitempty"`
	Owner             OwnerDetNameAndKindOnly `json:"uptreeOwner"`
	PodStatus         string                  `json:"podStatus"`
	CreationTimestamp string                  `json:"startedAt"`
	DeletionTimestamp string                  `json:"terminatedAt,omitempty"`
}

func NewPodDataForExistMicroService(pod *core.Pod, ownerDetNameAndKindOnly OwnerDetNameAndKindOnly, numberOfRunnigPods int, podStatus string) PodDataForExistMicroService {
	return PodDataForExistMicroService{
		PodName:   pod.ObjectMeta.Name,
		NodeName:  pod.Spec.NodeName,
		PodIP:     pod.Status.PodIP,
		Namespace: pod.ObjectMeta.Namespace,
		Owner:     ownerDetNameAndKindOnly,
		PodStatus: podStatus,
	}
}

// PodWatch - Stay updated starts infinite loop which will observe changes in pods so we can know if they changed and acts accordinally
func (wh *WatchHandler) PodWatch() {
	pStore := opapoliciesstore.NewPoliciyStore()
	policiesDir := os.Getenv("ARMO_POLICIES_DIR")
	if err := pStore.LoadRegoPoliciesFromDir(policiesDir); err != nil {
		glog.Error("Failed to LoadRegoPoliciesFromDir", policiesDir, err)
	}
	for {
		// defer func() {
		// 	if err := recover(); err != nil {
		// 		glog.Errorf("RECOVER PodWatch. error: %v", err)
		// 	}
		// }()
		glog.Infof("Watching over pods starting")
		podsWatcher, err := wh.RestAPIClient.CoreV1().Pods("").Watch(globalHTTPContext, metav1.ListOptions{Watch: true})
		if err != nil {
			glog.Errorf("Watch error: %s", err.Error())
		}
		for event := range podsWatcher.ResultChan() {
			pod, _ := event.Object.(*core.Pod)
			podName := pod.ObjectMeta.Name
			if res, err := pStore.Eval(pod); err != nil {
				glog.Errorf("pStore.Eval error: %s", err.Error())
			} else {
				if len(res) > 0 {
					for desIdx := range res {
						if res[desIdx].Alert {
							glog.Infof("Found OPA alert for pod '%s': %+v", podName, res)
						}
					}
					if err := opapoliciesstore.NotifyReceiver(res); err != nil {
						glog.Error("failed to NotifyReceiver", err)

					}
				}
			}
			if podName == "" {
				podName = pod.ObjectMeta.GenerateName
			}
			podStatus := getPodStatus(pod)
			switch event.Type {
			case watch.Added:
				glog.Infof("added. name: %s, status: %s", podName, podStatus)
				od, err := GetAncestorOfPod(pod, wh)
				if err != nil {
					glog.Errorf("%s, ignoring pod report", err.Error())
					break
				}
				first := true
				id, runnigPodNum := IsPodSpecAlreadyExist(pod, wh.pdm)
				// glog.Infof("dwertent -- Adding IsPodSpecAlreadyExist name: %s, id: %d, runnigPodNum: %d", podName, id, runnigPodNum)
				if runnigPodNum == 0 {
					// glog.Infof("dwertent -- Adding NEW pod name: %s, id: %d", podName, id)
					wh.pdm[id] = list.New()
					nms := MicroServiceData{Pod: pod, Owner: od, PodSpecId: id}
					wh.pdm[id].PushBack(nms)
					wh.jsonReport.AddToJsonFormat(nms, MICROSERVICES, CREATED)
					runnigPodNum = 1
				} else { // Check if pod is already reported
					if wh.pdm[id].Front() != nil {
						element := wh.pdm[id].Front().Next()
						for element != nil {
							if element.Value.(PodDataForExistMicroService).PodName == podName {
								// glog.Infof("dwertent -- Adding UPDATE pod name: %s, id: %d", podName, id)
								first = false
								break
							}
							element = element.Next()
						}
					}
				}
				if !first {
					break
				}
				// glog.Infof("reporting added. name: %s, status: %s", podName, podStatus)
				newPod := PodDataForExistMicroService{PodName: podName, NodeName: pod.Spec.NodeName, PodIP: pod.Status.PodIP, Namespace: pod.ObjectMeta.Namespace, Owner: OwnerDetNameAndKindOnly{Name: od.Name, Kind: od.Kind}, PodStatus: podStatus, CreationTimestamp: pod.CreationTimestamp.Time.UTC().Format(time.RFC3339)}
				wh.pdm[id].PushBack(newPod)
				wh.jsonReport.AddToJsonFormat(newPod, PODS, CREATED)
				informNewDataArrive(wh)

			case watch.Modified:
				if pod.DeletionTimestamp != nil { // the pod is terminating
					break
				}
				podSpecID, newPodData := wh.UpdatePod(pod, wh.pdm, podStatus)
				if podSpecID > -2 {
					glog.Infof("Modified. name: %s, status: %s", podName, podStatus)
					wh.jsonReport.AddToJsonFormat(newPodData, PODS, UPDATED)
				}
				if podSpecID > -1 {
					wh.jsonReport.AddToJsonFormat(wh.pdm[podSpecID].Front().Value.(MicroServiceData), MICROSERVICES, UPDATED)
				}
				if podSpecID > -2 {
					informNewDataArrive(wh)
				}
			case watch.Deleted:
				wh.DeletePod(pod, podName)
			case watch.Bookmark:
				glog.Infof("Bookmark. name: %s, status: %s", podName, podStatus)
			case watch.Error:
				glog.Infof("Error. name: %s, status: %s", podName, podStatus)
			}
		}

	}
}

// DeletePod delete a pod
func (wh *WatchHandler) DeletePod(pod *core.Pod, podName string) {
	podStatus := "Terminating"
	podSpecID, removeMicroServiceAsWell, owner := wh.RemovePod(pod, wh.pdm)
	if podSpecID == -1 {
		return
	}
	glog.Infof("Deleted. name: %s, status: %s", podName, podStatus)
	np := PodDataForExistMicroService{PodName: pod.ObjectMeta.Name, NodeName: pod.Spec.NodeName, PodIP: pod.Status.PodIP, Namespace: pod.ObjectMeta.Namespace, Owner: OwnerDetNameAndKindOnly{Name: owner.Name, Kind: owner.Kind}, PodStatus: podStatus, CreationTimestamp: pod.CreationTimestamp.Time.UTC().Format(time.RFC3339)}
	if pod.DeletionTimestamp != nil {
		np.DeletionTimestamp = pod.DeletionTimestamp.Time.UTC().Format(time.RFC3339)
	}
	wh.jsonReport.AddToJsonFormat(np, PODS, DELETED)
	if removeMicroServiceAsWell {
		glog.Infof("remove %s.%s", owner.Kind, owner.Name)
		nms := MicroServiceData{Pod: pod, Owner: owner, PodSpecId: podSpecID}
		wh.jsonReport.AddToJsonFormat(nms, MICROSERVICES, DELETED)
	}
	informNewDataArrive(wh)
}

// IsPodExist check
func IsPodExist(pod *core.Pod, pdm map[int]*list.List) bool {
	for _, v := range pdm {
		if v == nil || v.Len() == 0 {
			continue
		}
		if v.Front().Value.(MicroServiceData).Pod.ObjectMeta.Name == pod.ObjectMeta.Name {
			return true
		}
		if v.Front().Value.(MicroServiceData).Pod.ObjectMeta.GenerateName == pod.ObjectMeta.Name {
			return true
		}
		for e := ids.Ids.Front().Next(); e != nil; e = e.Next() {
			if e.Value.(PodDataForExistMicroService).PodName == pod.ObjectMeta.Name {
				return true
			}
		}
	}
	return false
}

// IsPodSpecAlreadyExist -
func IsPodSpecAlreadyExist(pod *core.Pod, pdm map[int]*list.List) (int, int) {
	for _, v := range pdm {
		if v == nil || v.Len() == 0 {
			continue
		}
		p := v.Front().Value.(MicroServiceData)
		//test owner references(if those exists)
		if p.ObjectMeta.UID == pod.ObjectMeta.UID || (p.ObjectMeta.Namespace == pod.ObjectMeta.Namespace &&
			(reflect.DeepEqual(p.OwnerReferences, pod.OwnerReferences))) {
			return v.Front().Value.(MicroServiceData).PodSpecId, v.Len()
		}
	}

	return CreateID(), 0
}

// METHOD NOT IN USE
// NumberOfRunningPods count number of running pods
func NumberOfRunningPods(pod *core.Pod, pdm map[int]*list.List) int {
	counter := 0
	for _, v := range pdm {
		if v == nil || v.Len() == 0 {
			continue
		}
		p := v.Front().Value.(MicroServiceData)
		//test owner references(if those exists)
		if p.ObjectMeta.UID == pod.ObjectMeta.UID || (p.ObjectMeta.Namespace == pod.ObjectMeta.Namespace &&
			(reflect.DeepEqual(p.OwnerReferences, pod.OwnerReferences))) {
			element := v.Front()
			for element != nil {
				if _, k := element.Value.(PodDataForExistMicroService); k {
					counter++
				}
				element = element.Next()
			}
			return counter
		}
	}

	return counter
}

// GetOwnerData - get the data of pod owner
func GetOwnerData(name string, kind string, apiVersion string, namespace string, wh *WatchHandler) interface{} {
	switch kind {
	case "Deployment":
		options := v1.GetOptions{}
		depDet, err := wh.RestAPIClient.AppsV1().Deployments(namespace).Get(globalHTTPContext, name, options)
		if err != nil {
			glog.Errorf("GetOwnerData Deployments: %s", err.Error())
			return nil
		}
		depDet.TypeMeta.Kind = kind
		depDet.TypeMeta.APIVersion = apiVersion
		return depDet
	case "DeamonSet", "DaemonSet":
		options := v1.GetOptions{}
		daemSetDet, err := wh.RestAPIClient.AppsV1().DaemonSets(namespace).Get(globalHTTPContext, name, options)
		if err != nil {
			glog.Errorf("GetOwnerData DaemonSets: %s", err.Error())
			return nil
		}
		daemSetDet.TypeMeta.Kind = kind
		daemSetDet.TypeMeta.APIVersion = apiVersion
		return daemSetDet
	case "StatefulSet":
		options := v1.GetOptions{}
		statSetDet, err := wh.RestAPIClient.AppsV1().StatefulSets(namespace).Get(globalHTTPContext, name, options)
		if err != nil {
			glog.Errorf("GetOwnerData StatefulSets: %s", err.Error())
			return nil
		}
		statSetDet.TypeMeta.Kind = kind
		statSetDet.TypeMeta.APIVersion = apiVersion
		return statSetDet
	case "Job":
		options := v1.GetOptions{}
		jobDet, err := wh.RestAPIClient.BatchV1().Jobs(namespace).Get(globalHTTPContext, name, options)
		if err != nil {
			glog.Errorf("GetOwnerData Jobs: %s", err.Error())
			return nil
		}
		jobDet.TypeMeta.Kind = kind
		jobDet.TypeMeta.APIVersion = apiVersion
		return jobDet
	case "CronJob":
		options := v1.GetOptions{}
		cronJobDet, err := wh.RestAPIClient.BatchV1beta1().CronJobs(namespace).Get(globalHTTPContext, name, options)
		if err != nil {
			glog.Errorf("GetOwnerData CronJobs: %s", err.Error())
			return nil
		}
		cronJobDet.TypeMeta.Kind = kind
		cronJobDet.TypeMeta.APIVersion = apiVersion
		return cronJobDet
	case "Pod":
		options := v1.GetOptions{}
		podDet, err := wh.RestAPIClient.CoreV1().Pods(namespace).Get(globalHTTPContext, name, options)
		if err != nil {
			glog.Errorf("GetOwnerData Pods: %s", err.Error())
			return nil
		}
		podDet.TypeMeta.Kind = kind
		podDet.TypeMeta.APIVersion = apiVersion
		return podDet

	default:
		if wh.extensionsClient == nil {
			return nil
		}
		options := v1.ListOptions{}
		crds, err := wh.extensionsClient.CustomResourceDefinitions().List(context.Background(), options)
		if err != nil {
			glog.Errorf("GetOwnerData CustomResourceDefinitions: %s", err.Error())
			return nil
		}
		for crdIdx := range crds.Items {
			if crds.Items[crdIdx].Status.AcceptedNames.Kind == kind {
				return CRDOwnerData{
					v1.TypeMeta{Kind: crds.Items[crdIdx].Kind,
						APIVersion: apiVersion,
					}}
			}
		}
	}

	return nil
}

// GetAncestorOfPod -
func GetAncestorOfPod(pod *core.Pod, wh *WatchHandler) (OwnerDet, error) {
	od := OwnerDet{}

	if pod.OwnerReferences != nil {
		switch pod.OwnerReferences[0].Kind {
		case "ReplicaSet":
			repItem, err := wh.RestAPIClient.AppsV1().ReplicaSets(pod.ObjectMeta.Namespace).Get(globalHTTPContext, pod.OwnerReferences[0].Name, metav1.GetOptions{})
			if err != nil {
				return od, fmt.Errorf("error getting owner reference: %s", err.Error())
			}
			if repItem.OwnerReferences != nil {
				od.Name = repItem.OwnerReferences[0].Name
				od.Kind = repItem.OwnerReferences[0].Kind
				//meanwhile owner reference must be in the same namespace, so owner reference doesn't have the namespace field(may be changed in the future)
				od.OwnerData = GetOwnerData(repItem.OwnerReferences[0].Name, repItem.OwnerReferences[0].Kind, repItem.OwnerReferences[0].APIVersion, pod.ObjectMeta.Namespace, wh)
			} else {
				depInt := wh.RestAPIClient.AppsV1().Deployments(pod.ObjectMeta.Namespace)
				selector, err := metav1.LabelSelectorAsSelector(repItem.Spec.Selector)
				if err != nil {
					return od, fmt.Errorf("error getting owner reference: %s", err.Error())
				}

				options := metav1.ListOptions{}
				depList, _ := depInt.List(globalHTTPContext, options)
				for _, item := range depList.Items {
					if selector.Empty() || !selector.Matches(labels.Set(pod.Labels)) {
						continue
					} else {
						od.Name = item.ObjectMeta.Name
						od.Kind = item.Kind
						od.OwnerData = GetOwnerData(od.Name, od.Kind, item.TypeMeta.APIVersion, pod.ObjectMeta.Namespace, wh)
						break
					}
				}
			}
		case "Job":
			jobItem, err := wh.RestAPIClient.BatchV1().Jobs(pod.ObjectMeta.Namespace).Get(globalHTTPContext, pod.OwnerReferences[0].Name, metav1.GetOptions{})
			if err != nil {
				glog.Error(err)
				return od, err
			}
			if jobItem.OwnerReferences != nil {
				od.Name = jobItem.OwnerReferences[0].Name
				od.Kind = jobItem.OwnerReferences[0].Kind
				//meanwhile owner reference must be in the same namespace, so owner reference doesn't have the namespace field(may be changed in the future)
				od.OwnerData = GetOwnerData(jobItem.OwnerReferences[0].Name, jobItem.OwnerReferences[0].Kind, jobItem.OwnerReferences[0].APIVersion, pod.ObjectMeta.Namespace, wh)
				break
			}

			depList, _ := wh.RestAPIClient.BatchV1beta1().CronJobs(pod.ObjectMeta.Namespace).List(globalHTTPContext, metav1.ListOptions{})
			selector, err := metav1.LabelSelectorAsSelector(jobItem.Spec.Selector)
			if err != nil {
				glog.Errorf("LabelSelectorAsSelector: %s", err.Error())
				return od, fmt.Errorf("error getting owner reference")
			}

			for _, item := range depList.Items {
				if selector.Empty() || !selector.Matches(labels.Set(pod.Labels)) {
					continue
				} else {
					od.Name = item.ObjectMeta.Name
					od.Kind = item.Kind
					od.OwnerData = GetOwnerData(od.Name, od.Kind, item.TypeMeta.APIVersion, pod.ObjectMeta.Namespace, wh)
					break
				}
			}

		default: // POD
			od.Name = pod.OwnerReferences[0].Name
			od.Kind = pod.OwnerReferences[0].Kind
			od.OwnerData = GetOwnerData(pod.OwnerReferences[0].Name, pod.OwnerReferences[0].Kind, pod.OwnerReferences[0].APIVersion, pod.ObjectMeta.Namespace, wh)
		}
	} else {
		od.Name = pod.ObjectMeta.Name
		od.Kind = "Pod"
		od.OwnerData = GetOwnerData(pod.ObjectMeta.Name, od.Kind, pod.APIVersion, pod.ObjectMeta.Namespace, wh)
		if crd, ok := od.OwnerData.(CRDOwnerData); ok {
			od.Kind = crd.Kind
		}
	}
	return od, nil
}

func (wh *WatchHandler) UpdatePod(pod *core.Pod, pdm map[int]*list.List, podStatus string) (int, PodDataForExistMicroService) {
	id := -2
	podDataForExistMicroService := PodDataForExistMicroService{}
	for _, v := range pdm {
		// glog.Infof("dwertent -- Modified UpdatePod name: %s, id: %d", jj)
		element := v.Front().Next()
		for element != nil {
			if strings.Compare(element.Value.(PodDataForExistMicroService).PodName, pod.ObjectMeta.Name) == 0 {
				// newOwner := GetAncestorOfPod(pod, wh)

				if reflect.DeepEqual(*v.Front().Value.(MicroServiceData).Pod, *pod) {
					err := DeepCopy(*pod, *v.Front().Value.(MicroServiceData).Pod)
					if err != nil {
						glog.Errorf("error in DeepCopy 'Pod' in UpdatePod")
					}
					// err = DeepCopy(newOwner, v.Front().Value.(MicroServiceData).Owner)
					// if err != nil {
					// 	glog.Errorf("error in DeepCopy B in UpdatePod")
					// }
					id = v.Front().Value.(MicroServiceData).PodSpecId
				} else {
					id = -1
				}
				podDataForExistMicroService = PodDataForExistMicroService{PodName: pod.ObjectMeta.Name, NodeName: pod.Spec.NodeName, PodIP: pod.Status.PodIP, Namespace: pod.ObjectMeta.Namespace, PodStatus: podStatus, CreationTimestamp: pod.CreationTimestamp.Time.UTC().Format(time.RFC3339)}

				if err := DeepCopy(element.Value.(PodDataForExistMicroService).Owner, &podDataForExistMicroService.Owner); err != nil {
					glog.Errorf("error in DeepCopy 'Owner' in UpdatePod")
				}

				if err := DeepCopyObj(podDataForExistMicroService, element.Value.(PodDataForExistMicroService)); err != nil {
					glog.Errorf("error in DeepCopy 'PodDataForExistMicroService' in UpdatePod")
				}
				break
			}
			element = element.Next()
		}
	}
	return id, podDataForExistMicroService
}

func (wh *WatchHandler) isMicroServiceNeedToBeRemoved(ownerData interface{}, kind, namespace string) bool {
	switch kind {
	case "Deployment":
		options := v1.GetOptions{}
		name := ownerData.(*appsv1.Deployment).ObjectMeta.Name
		mic, err := wh.RestAPIClient.AppsV1().Deployments(namespace).Get(globalHTTPContext, name, options)
		if errors.IsNotFound(err) {
			return true
		}
		v, _ := json.Marshal(mic)
		glog.Infof("Removing pod but not Deployment: %s", string(v))

	case "DeamonSet", "DaemonSet":
		options := v1.GetOptions{}
		name := ownerData.(*appsv1.DaemonSet).ObjectMeta.Name
		mic, err := wh.RestAPIClient.AppsV1().DaemonSets(namespace).Get(globalHTTPContext, name, options)
		if errors.IsNotFound(err) {
			return true
		}
		v, _ := json.Marshal(mic)
		glog.Infof("Removing pod but not DaemonSet: %s", string(v))

	case "StatefulSets":
		options := v1.GetOptions{}
		name := ownerData.(*appsv1.StatefulSet).ObjectMeta.Name
		mic, err := wh.RestAPIClient.AppsV1().StatefulSets(namespace).Get(globalHTTPContext, name, options)
		if errors.IsNotFound(err) {
			return true
		}
		v, _ := json.Marshal(mic)
		glog.Infof("Removing pod but not StatefulSet: %s", string(v))
	case "Job":
		options := v1.GetOptions{}
		name := ownerData.(*batchv1.Job).ObjectMeta.Name
		mic, err := wh.RestAPIClient.BatchV1().Jobs(namespace).Get(globalHTTPContext, name, options)
		if errors.IsNotFound(err) {
			return true
		}
		v, _ := json.Marshal(mic)
		glog.Infof("Removing pod but not Job: %s", string(v))
	case "CronJob":
		options := v1.GetOptions{}
		cronJob, ok := ownerData.(*v1beta1.CronJob)
		if !ok {
			glog.Errorf("cant convert to v1beta1.CronJob")
			return true
		}
		mic, err := wh.RestAPIClient.BatchV1beta1().CronJobs(namespace).Get(globalHTTPContext, cronJob.ObjectMeta.Name, options)
		if errors.IsNotFound(err) {
			return true
		}
		v, _ := json.Marshal(mic)
		glog.Infof("Removing pod but not CronJob: %s", string(v))
	case "Pod":
		options := v1.GetOptions{}
		name := ownerData.(*core.Pod).ObjectMeta.Name
		mic, err := wh.RestAPIClient.CoreV1().Pods(namespace).Get(globalHTTPContext, name, options)
		if errors.IsNotFound(err) {
			return true
		}
		v, _ := json.Marshal(mic)
		glog.Infof("Removing pod but not Pod: %s", string(v))
	}

	return false
}

// RemovePod remove pod and check if has parents
func (wh *WatchHandler) RemovePod(pod *core.Pod, pdm map[int]*list.List) (int, bool, OwnerDet) {
	var owner OwnerDet
	for id, v := range pdm {
		if v.Front() != nil {
			element := v.Front().Next()
			for element != nil {
				if element.Value.(PodDataForExistMicroService).PodName == pod.ObjectMeta.Name {
					//log.Printf("microservice %s removed\n", element.Value.(PodDataForExistMicroService).PodName)
					owner = v.Front().Value.(MicroServiceData).Owner
					v.Remove(element)
					removed := false
					if v.Len() == 1 {
						msd := v.Front().Value.(MicroServiceData)
						removed = wh.isMicroServiceNeedToBeRemoved(msd.Owner.OwnerData, msd.Owner.Kind, msd.ObjectMeta.Namespace)
						podSpecID := v.Front().Value.(MicroServiceData).PodSpecId
						if removed {
							v.Remove(v.Front())
							delete(pdm, id)
						}
						return podSpecID, removed, owner
					}
					// remove before testing len?
					return v.Front().Value.(MicroServiceData).PodSpecId, removed, owner
				}
				if element.Value.(PodDataForExistMicroService).PodName == pod.ObjectMeta.GenerateName {
					//log.Printf("microservice %s removed\n", element.Value.(PodDataForExistMicroService).PodName)
					owner = v.Front().Value.(MicroServiceData).Owner
					removed := false
					v.Remove(element)
					if v.Len() == 1 {
						msd := v.Front().Value.(MicroServiceData)
						removed := wh.isMicroServiceNeedToBeRemoved(msd.Owner.OwnerData, msd.Owner.Kind, msd.ObjectMeta.Namespace)
						podSpecID := v.Front().Value.(MicroServiceData).PodSpecId
						if removed {
							v.Remove(v.Front())
							delete(pdm, id)
						}
						return podSpecID, removed, owner
					}
					return v.Front().Value.(MicroServiceData).PodSpecId, removed, owner
				}
				element = element.Next()
			}
		}
	}
	return -1, false, owner
}

// func (wh *WatchHandler) AddPod(pod *core.Pod, pdm map[int]*list.List) (int, int, bool, OwnerDet) {

// }
func getPodStatus(pod *core.Pod) string {
	containerStatuses := pod.Status.ContainerStatuses
	status := ""
	if len(containerStatuses) > 0 {
		for i := range containerStatuses {
			if containerStatuses[i].State.Terminated != nil {
				status = containerStatuses[i].State.Terminated.Reason
			}
			if containerStatuses[i].State.Waiting != nil {
				status = containerStatuses[i].State.Waiting.Reason
			}
			if containerStatuses[i].State.Running != nil {
				if status == "" { // if none of the conatainers report a error
					status = "Running"
				}
			}
			// if pod.Namespace == "default" || pod.Namespace == "" {
			// 	glog.Infof("----------------------------------------------------------------------------------------------------")
			// 	neww, _ := json.Marshal(containerStatuses[i].State)
			// 	glog.Infof("dwertent, containerStatuses: %s", string(neww))
			// 	glog.Infof("----------------------------------------------------------------------------------------------------")
			// }
		}
	}
	if status == "" {
		status = string(pod.Status.Phase)
	}
	return status
}

// func (wh *WatchHandler) waitPodStateUpdate(pod *core.Pod) *core.Pod {
// 	// begin := time.Now()
// 	// log.Printf("waiting for pod %v enter desired state\n", pod.ObjectMeta.Name)
// 	latestPodState := pod.Status.Phase

// 	for {
// 		desiredStatePod, err := wh.RestAPIClient.CoreV1().Pods(pod.ObjectMeta.Namespace).Get(globalHTTPContext, pod.ObjectMeta.Name, metav1.GetOptions{})
// 		if err != nil {
// 			log.Printf("podEnterDesiredState fail while we Get the pod %v\n", pod.ObjectMeta.Name)
// 			return nil
// 		}
// 		if desiredStatePod.Status.Phase != latestPodState {
// 			return desiredStatePod
// 		}
// 		// if desiredStatePod.Namespace == "default" || desiredStatePod.Namespace == "" {
// 		// 	podd, _ := json.Marshal(desiredStatePod)
// 		// 	glog.Infof("dwertent, Status: %s, desiredStatePod: %s", string(desiredStatePod.Status.Phase), string(podd))
// 		// }
// 		// if desiredStatePod.Status.Phase == core.PodRunning || strings.Compare(string(desiredStatePod.Status.Phase), string(core.PodSucceeded)) == 0 {
// 		// 	log.Printf("pod %v enter desired state\n", pod.ObjectMeta.Name)
// 		// 	return desiredStatePod, true
// 		// } else if strings.Compare(string(desiredStatePod.Status.Phase), string(core.PodFailed)) == 0 || strings.Compare(string(desiredStatePod.Status.Phase), string(core.PodUnknown)) == 0 {
// 		// 	log.Printf("pod %v State is %v\n", pod.ObjectMeta.Name, pod.Status.Phase)
// 		// 	return desiredStatePod, true
// 		// } else {
// 		// 	if time.Now().Sub(begin) > 5*time.Minute {
// 		// 		log.Printf("we wait for 5 nimutes pod %v to change his state to desired state and it's too long\n", pod.ObjectMeta.Name)
// 		// 		return nil, false
// 		// 	}
// 		// }
// 	}
// }
