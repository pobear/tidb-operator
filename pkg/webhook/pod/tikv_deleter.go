// Copyright 2019 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// See the License for the specific language governing permissions and
// limitations under the License.

package pod

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/pingcap/tidb-operator/pkg/apis/pingcap/v1alpha1"
	"github.com/pingcap/tidb-operator/pkg/label"
	"github.com/pingcap/tidb-operator/pkg/pdapi"
	operatorUtils "github.com/pingcap/tidb-operator/pkg/util"
	"github.com/pingcap/tidb-operator/pkg/webhook/util"
	admission "k8s.io/api/admission/v1"
	"k8s.io/klog"
)

const (
	// EvictLeaderBeginTime is the key of evict Leader begin time
	EvictLeaderBeginTime = label.AnnEvictLeaderBeginTime

	tikvStoreNotFoundPattern = `"invalid store ID %d, not found"`
)

var (
	// EvictLeaderTimeout is the timeout limit of evict leader
	EvictLeaderTimeout time.Duration
)

func (pc *PodAdmissionControl) admitDeleteTiKVPods(payload *admitPayload) *admission.AdmissionResponse {

	pod := payload.pod
	tc := payload.tc
	pdClient := payload.pdClient

	name := pod.Name
	namespace := pod.Namespace
	tcName := tc.Name

	storesInfo, err := pdClient.GetStores()
	if err != nil {
		return util.ARFail(err)
	}

	var storeInfo *pdapi.StoreInfo

	storeId, existed := pod.Labels[label.StoreIDLabelKey]
	if existed {
		storeIdInt, err := strconv.Atoi(storeId)
		if err != nil {
			klog.Infof("tc[%s/%s]'s tikv pod[%s/%s] failed to delete,%v", namespace, tcName, namespace, name, err)
			return util.ARFail(err)
		}
		// find storeInfo by storeId, if error was not found, deal with it by admitDeleteUselessTiKVPod func
		storeInfo, err = pdClient.GetStore(uint64(storeIdInt))
		if err != nil && !strings.HasSuffix(err.Error(), fmt.Sprintf(tikvStoreNotFoundPattern+"\n", storeIdInt)) {
			klog.Infof("tc[%s/%s]'s tikv pod[%s/%s] failed to delete,%v", namespace, tcName, namespace, name, err)
			return util.ARFail(err)
		}
	}

	if storeInfo == nil || storeInfo.Store == nil {
		klog.Infof("tc[%s/%s]'s tikv pod[%s/%s] can't be found store", namespace, tcName, namespace, name)
		return pc.admitDeleteUselessTiKVPod(payload)
	}

	switch storeInfo.Store.StateName {
	case v1alpha1.TiKVStateTombstone:
		return pc.admitDeleteUselessTiKVPod(payload)
	case v1alpha1.TiKVStateOffline:
		return pc.admitDeleteOfflineTiKVPod()
	case v1alpha1.TiKVStateDown:
		return pc.admitDeleteUselessTiKVPod(payload)
	case v1alpha1.TiKVStateUp:
		return pc.admitDeleteUpTiKVPod(payload, storeInfo, storesInfo)
	default:
		klog.Infof("unknown store state[%s] for tikv pod[%s/%s]", storeInfo.Store.StateName, namespace, name)
	}

	return &admission.AdmissionResponse{
		Allowed: false,
	}
}

func (pc *PodAdmissionControl) admitDeleteUselessTiKVPod(payload *admitPayload) *admission.AdmissionResponse {

	name := payload.pod.Name
	namespace := payload.pod.Namespace
	isInOrdinal, err := operatorUtils.IsPodOrdinalNotExceedReplicas(payload.pod, *payload.ownerStatefulSet.Spec.Replicas)
	if err != nil {
		return util.ARFail(err)
	}
	ordinal, err := operatorUtils.GetOrdinalFromPodName(name)
	if err != nil {
		return util.ARFail(err)
	}
	tcName := payload.tc.Name

	if !isInOrdinal {
		pvcName := operatorUtils.OrdinalPVCName(v1alpha1.TiKVMemberType, payload.ownerStatefulSet.Name, ordinal)
		pvc, err := pc.pvcControl.GetPVC(pvcName, namespace)
		if err != nil {
			return util.ARFail(err)
		}
		err = addDeferDeletingToPVC(pvc, pc.pvcControl, payload.tc)
		if err != nil {
			klog.Infof("tc[%s/%s]'s tikv pod[%s/%s] failed to delete,%v", namespace, tcName, namespace, name, err)
			return util.ARFail(err)
		}
	}

	return util.ARSuccess()
}

func (pc *PodAdmissionControl) admitDeleteOfflineTiKVPod() *admission.AdmissionResponse {
	return &admission.AdmissionResponse{
		Allowed: false,
	}
}

func (pc *PodAdmissionControl) admitDeleteUpTiKVPod(payload *admitPayload, store *pdapi.StoreInfo, storesInfo *pdapi.StoresInfo) *admission.AdmissionResponse {

	name := payload.pod.Name
	namespace := payload.pod.Namespace
	isInOrdinal, err := operatorUtils.IsPodOrdinalNotExceedReplicas(payload.pod, *payload.ownerStatefulSet.Spec.Replicas)
	if err != nil {
		return util.ARFail(err)
	}
	ordinal, err := operatorUtils.GetOrdinalFromPodName(name)
	if err != nil {
		return util.ARFail(err)
	}
	tcName := payload.tc.Name
	isUpgrading := IsStatefulSetUpgrading(payload.ownerStatefulSet)

	if !isInOrdinal {
		err = payload.pdClient.DeleteStore(store.Store.Id)
		if err != nil {
			klog.Infof("tc[%s/%s]'s tikv pod[%s/%s] failed to delete,%v", namespace, tcName, namespace, name, err)
			return util.ARFail(err)
		}
		return &admission.AdmissionResponse{
			Allowed: false,
		}
	}

	if isUpgrading {
		err = checkFormerTiKVPodStatus(pc.podLister, payload.tc, ordinal, *payload.ownerStatefulSet.Spec.Replicas, storesInfo)
		if err != nil {
			klog.Infof("tc[%s/%s]'s tikv pod[%s/%s] failed to delete,%v", namespace, tcName, namespace, name, err)
			return util.ARFail(err)
		}
		return pc.admitDeleteUpTiKVPodDuringUpgrading(payload, store)
	}

	return util.ARSuccess()
}

func (pc *PodAdmissionControl) admitDeleteUpTiKVPodDuringUpgrading(payload *admitPayload, store *pdapi.StoreInfo) *admission.AdmissionResponse {

	name := payload.pod.Name
	namespace := payload.pod.Namespace
	tcName := payload.tc.Name

	_, evicting := payload.pod.Annotations[EvictLeaderBeginTime]
	if !evicting {
		err := beginEvictLeader(pc.kubeCli, store.Store.Id, payload.pod, payload.pdClient)
		if err != nil {
			klog.Infof("tc[%s/%s]'s tikv pod[%s/%s] failed to delete,%v", namespace, tcName, namespace, name, err)
			return util.ARFail(err)
		}
		return &admission.AdmissionResponse{
			Allowed: false,
		}
	}

	if !isTiKVReadyToUpgrade(payload.pod, store) {
		return &admission.AdmissionResponse{
			Allowed: false,
		}
	}

	return util.ARSuccess()
}
