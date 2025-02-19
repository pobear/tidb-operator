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

package statefulset

import (
	"errors"
	"fmt"
	"strconv"

	asappsv1alpha1 "github.com/pingcap/advanced-statefulset/pkg/apis/apps/v1alpha1"
	"github.com/pingcap/tidb-operator/pkg/client/clientset/versioned"
	"github.com/pingcap/tidb-operator/pkg/controller"
	"github.com/pingcap/tidb-operator/pkg/features"
	"github.com/pingcap/tidb-operator/pkg/label"
	"github.com/pingcap/tidb-operator/pkg/webhook/util"
	admission "k8s.io/api/admission/v1"
	apps "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	glog "k8s.io/klog"
)

var (
	versionCli   versioned.Interface
	deserializer runtime.Decoder
)

func init() {
	deserializer = util.GetCodec()
}

func AdmitStatefulSets(ar admission.AdmissionReview) *admission.AdmissionResponse {

	name := ar.Request.Name
	namespace := ar.Request.Namespace

	expectedGroup := "apps"
	if features.DefaultFeatureGate.Enabled(features.AdvancedStatefulSet) {
		expectedGroup = asappsv1alpha1.GroupName
	}
	apiVersion := ar.Request.Resource.Version
	setResource := metav1.GroupVersionResource{Group: expectedGroup, Version: apiVersion, Resource: "statefulsets"}
	if ar.Request.Resource.Group != setResource.Group || ar.Request.Resource.Resource != setResource.Resource {
		err := fmt.Errorf("expect resource to be %s instead of %s", setResource, ar.Request.Resource)
		glog.Error(err)
		return util.ARFail(err)
	}
	glog.V(4).Infof("admit %s [%s/%s]", setResource, namespace, name)

	if versionCli == nil {
		cfg, err := rest.InClusterConfig()
		if err != nil {
			err := fmt.Errorf("statefulset %s/%s, get k8s cluster config failed, err: %v", namespace, name, err)
			glog.Errorf(err.Error())
			return util.ARFail(err)
		}

		versionCli, err = versioned.NewForConfig(cfg)
		if err != nil {
			err := fmt.Errorf("statefulset %s/%s, create Clientset failed, err: %v", namespace, name, err)
			glog.Errorf(err.Error())
			return util.ARFail(err)
		}
	}

	stsObjectMeta, stsPartition, err := getStsAttributes(ar.Request.OldObject.Raw, apiVersion)
	if err != nil {
		err = fmt.Errorf("statefulset %s/%s, decode request failed, err: %v", namespace, name, err)
		glog.Error(err)
		return util.ARFail(err)
	}

	l := label.Label(stsObjectMeta.Labels)

	if !(l.IsTiDB() || l.IsTiKV()) {
		// If it is not statefulset of tikv and tidb, return quickly.
		return util.ARSuccess()
	}

	controllerRef := metav1.GetControllerOf(stsObjectMeta)
	if controllerRef == nil || controllerRef.Kind != controller.ControllerKind.Kind {
		// In this case, we can't tell if this statefulset is controlled by tidb-operator,
		// so we don't block this statefulset upgrade, return directly.
		glog.Warningf("statefulset %s/%s has tidb or tikv component label but doesn't have owner reference or the owner reference is not TidbCluster", namespace, name)
		return util.ARSuccess()
	}

	tcName := controllerRef.Name
	tc, err := versionCli.PingcapV1alpha1().TidbClusters(namespace).Get(tcName, metav1.GetOptions{})
	if err != nil {
		err := fmt.Errorf("get tidbcluster %s/%s failed, statefulset %s, err %v", namespace, tcName, name, err)
		glog.Errorf(err.Error())
		return util.ARFail(err)
	}

	var partitionStr string
	partitionStr = tc.Annotations[label.AnnTiDBPartition]
	if l.IsTiKV() {
		partitionStr = tc.Annotations[label.AnnTiKVPartition]
	}

	if len(partitionStr) == 0 {
		return util.ARSuccess()
	}

	partition, err := strconv.ParseInt(partitionStr, 10, 32)
	if err != nil {
		err := fmt.Errorf("statefulset %s/%s, convert partition str %s to int failed, err: %v", namespace, name, partitionStr, err)
		glog.Errorf(err.Error())
		return util.ARFail(err)
	}

	setPartition := *(stsPartition)
	if setPartition > 0 && setPartition <= int32(partition) {
		glog.V(4).Infof("statefulset %s/%s has been protect by partition %s annotations", namespace, name, partitionStr)
		return util.ARFail(errors.New("protect by partition annotation"))
	}
	glog.Infof("admit statefulset %s/%s update partition to %d, protect partition is %d", namespace, name, setPartition, partition)
	return util.ARSuccess()
}

func getStsAttributes(data []byte, apiVersion string) (*metav1.ObjectMeta, *int32, error) {
	if apiVersion == "v1" {
		set := apps.StatefulSet{}
		if _, _, err := deserializer.Decode(data, nil, &set); err != nil {
			return nil, nil, err
		}
		return &(set.ObjectMeta), set.Spec.UpdateStrategy.RollingUpdate.Partition, nil
	}

	set := apps.StatefulSet{}
	if _, _, err := deserializer.Decode(data, nil, &set); err != nil {
		return nil, nil, err
	}
	return &(set.ObjectMeta), set.Spec.UpdateStrategy.RollingUpdate.Partition, nil
}
