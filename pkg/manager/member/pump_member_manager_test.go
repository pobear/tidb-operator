// Copyright 2019. PingCAP, Inc.
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

package member

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	. "github.com/onsi/gomega"
	"github.com/pingcap/tidb-operator/pkg/apis/pingcap/v1alpha1"
	"github.com/pingcap/tidb-operator/pkg/client/clientset/versioned/fake"
	informers "github.com/pingcap/tidb-operator/pkg/client/informers/externalversions"
	"github.com/pingcap/tidb-operator/pkg/controller"
	"github.com/pingcap/tidb-operator/pkg/label"
	"github.com/pingcap/tidb-operator/pkg/util/config"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	kubeinformers "k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
)

func TestPumpMemberManagerSyncCreate(t *testing.T) {
	g := NewGomegaWithT(t)

	type result struct {
		sync   error
		svc    *corev1.Service
		getSvc error
		set    *appsv1.StatefulSet
		getSet error
		cm     *corev1.ConfigMap
		getCm  error
	}

	type testcase struct {
		name           string
		prepare        func(cluster *v1alpha1.TidbCluster)
		errOnCreateSet bool
		errOnCreateCm  bool
		errOnCreateSvc bool
		expectFn       func(*GomegaWithT, *result)
	}

	testFn := func(test *testcase, t *testing.T) {
		t.Log(test.name)
		tc := newTidbClusterForPump()
		ns := tc.Namespace
		tcName := tc.Name
		if test.prepare != nil {
			test.prepare(tc)
		}

		pmm, ctls, _ := newFakePumpMemberManager()

		if test.errOnCreateSet {
			ctls.set.SetCreateStatefulSetError(errors.NewInternalError(fmt.Errorf("API server failed")), 0)
		}
		if test.errOnCreateSvc {
			ctls.svc.SetCreateServiceError(errors.NewInternalError(fmt.Errorf("API server failed")), 0)
		}
		if test.errOnCreateCm {
			ctls.cm.SetCreateConfigMapError(errors.NewInternalError(fmt.Errorf("API server failed")), 0)
		}

		err := pmm.Sync(tc)
		svc, getSvcErr := pmm.svcLister.Services(ns).Get(controller.PumpPeerMemberName(tcName))
		set, getStsErr := pmm.setLister.StatefulSets(ns).Get(controller.PumpMemberName(tcName))
		cm, getCmErr := pmm.cmLister.ConfigMaps(ns).Get(controller.PumpMemberName(tcName))
		result := result{err, svc, getSvcErr, set, getStsErr, cm, getCmErr}
		test.expectFn(g, &result)
	}

	tests := []*testcase{
		{
			name:           "basic",
			prepare:        nil,
			errOnCreateSet: false,
			errOnCreateCm:  false,
			errOnCreateSvc: false,
			expectFn: func(g *GomegaWithT, r *result) {
				g.Expect(r.sync).To(Succeed())
				g.Expect(r.getCm).To(Succeed())
				g.Expect(r.getSet).To(Succeed())
				g.Expect(r.getSvc).To(Succeed())
			},
		},
		{
			name: "do not sync if pum spec is nil",
			prepare: func(tc *v1alpha1.TidbCluster) {
				tc.Spec.Pump = nil
			},
			errOnCreateSet: false,
			errOnCreateCm:  false,
			errOnCreateSvc: false,
			expectFn: func(g *GomegaWithT, r *result) {
				g.Expect(r.sync).To(Succeed())
				g.Expect(r.getCm).NotTo(Succeed())
				g.Expect(r.getSet).NotTo(Succeed())
				g.Expect(r.getSvc).NotTo(Succeed())
			},
		},
		{
			name: "pump storage format is wrong",
			prepare: func(tc *v1alpha1.TidbCluster) {
				tc.Spec.Pump.Requests.Storage = "100xxxxi"
			},
			errOnCreateSet: false,
			errOnCreateCm:  false,
			errOnCreateSvc: false,
			expectFn: func(g *GomegaWithT, r *result) {
				g.Expect(r.sync).NotTo(Succeed())
				g.Expect(r.sync.Error()).To(ContainSubstring("cant' parse storage size: 100xxxxi"))
			},
		},
		{
			name:           "error when create pump statefulset",
			prepare:        nil,
			errOnCreateSet: true,
			errOnCreateCm:  false,
			errOnCreateSvc: false,
			expectFn: func(g *GomegaWithT, r *result) {
				g.Expect(r.sync).NotTo(Succeed())
				g.Expect(r.getSet).NotTo(Succeed())
				g.Expect(r.getCm).To(Succeed())
				g.Expect(r.getSvc).To(Succeed())
			},
		},
		{
			name:           "error when create pump peer service",
			prepare:        nil,
			errOnCreateSet: false,
			errOnCreateCm:  false,
			errOnCreateSvc: true,
			expectFn: func(g *GomegaWithT, r *result) {
				g.Expect(r.sync).NotTo(Succeed())
				g.Expect(r.getSet).NotTo(Succeed())
				g.Expect(r.getCm).NotTo(Succeed())
				g.Expect(r.getSvc).NotTo(Succeed())
			},
		},
		{
			name:           "error when create pump configmap",
			prepare:        nil,
			errOnCreateSet: false,
			errOnCreateCm:  true,
			errOnCreateSvc: false,
			expectFn: func(g *GomegaWithT, r *result) {
				g.Expect(r.sync).NotTo(Succeed())
				g.Expect(r.getSet).NotTo(Succeed())
				g.Expect(r.getCm).NotTo(Succeed())
				g.Expect(r.getSvc).To(Succeed())
			},
		},
	}

	for _, tt := range tests {
		testFn(tt, t)
	}
}

func TestPumpMemberManagerSyncUpdate(t *testing.T) {
	g := NewGomegaWithT(t)

	type result struct {
		sync   error
		oldSvc *corev1.Service
		svc    *corev1.Service
		getSvc error
		oldSet *appsv1.StatefulSet
		set    *appsv1.StatefulSet
		getSet error
		oldCm  *corev1.ConfigMap
		cm     *corev1.ConfigMap
		getCm  error
	}
	type testcase struct {
		name           string
		prepare        func(*v1alpha1.TidbCluster, *pumpFakeIndexers)
		errOnUpdateSet bool
		errOnUpdateCm  bool
		errOnUpdateSvc bool
		expectFn       func(*GomegaWithT, *result)
	}

	testFn := func(test *testcase, t *testing.T) {
		t.Log(test.name)

		tc := newTidbClusterForPump()
		ns := tc.Namespace
		tcName := tc.Name

		pmm, ctls, indexers := newFakePumpMemberManager()

		if test.errOnUpdateSet {
			ctls.set.SetUpdateStatefulSetError(errors.NewInternalError(fmt.Errorf("API server failed")), 0)
		}
		if test.errOnUpdateSvc {
			ctls.svc.SetUpdateServiceError(errors.NewInternalError(fmt.Errorf("API server failed")), 0)
		}
		if test.errOnUpdateCm {
			ctls.cm.SetUpdateConfigMapError(errors.NewInternalError(fmt.Errorf("API server failed")), 0)
		}

		oldCm, err := getNewPumpConfigMap(tc)
		g.Expect(err).To(Succeed())
		oldSvc := getNewPumpHeadlessService(tc)
		oldSvc.Spec.Ports[0].Port = 8888
		oldSet, err := getNewPumpStatefulSet(tc, oldCm)
		g.Expect(err).To(Succeed())

		g.Expect(indexers.set.Add(oldSet)).To(Succeed())
		g.Expect(indexers.svc.Add(oldSvc)).To(Succeed())
		g.Expect(indexers.cm.Add(oldCm)).To(Succeed())

		if test.prepare != nil {
			test.prepare(tc, indexers)
		}

		err = pmm.Sync(tc)
		svc, getSvcErr := pmm.svcLister.Services(ns).Get(controller.PumpPeerMemberName(tcName))
		set, getStsErr := pmm.setLister.StatefulSets(ns).Get(controller.PumpMemberName(tcName))
		cm, getCmErr := pmm.cmLister.ConfigMaps(ns).Get(controller.PumpMemberName(tcName))
		result := result{err, oldSvc, svc, getSvcErr, oldSet, set, getStsErr, oldCm, cm, getCmErr}
		test.expectFn(g, &result)
	}

	tests := []*testcase{
		{
			name: "basic",
			prepare: func(tc *v1alpha1.TidbCluster, _ *pumpFakeIndexers) {
				tc.Spec.Pump.GenericConfig = config.New(map[string]interface{}{
					"gc": 6,
					"storage": map[string]interface{}{
						"stop-write-at-available-space": "10Gi",
					},
				})
				tc.Spec.Pump.Replicas = 5
			},
			errOnUpdateCm:  false,
			errOnUpdateSvc: false,
			errOnUpdateSet: false,
			expectFn: func(g *GomegaWithT, r *result) {
				g.Expect(r.sync).To(Succeed())
				g.Expect(r.svc.Spec.Ports[0].Port).NotTo(Equal(int32(8888)))
				g.Expect(r.cm.Data["pump-config"]).To(ContainSubstring("stop-write-at-available-space"))
				g.Expect(*r.set.Spec.Replicas).To(Equal(int32(5)))
			},
		},
		{
			name: "error on update configmap",
			prepare: func(tc *v1alpha1.TidbCluster, _ *pumpFakeIndexers) {
				tc.Spec.Pump.GenericConfig = config.New(map[string]interface{}{
					"gc": 6,
					"storage": map[string]interface{}{
						"stop-write-at-available-space": "10Gi",
					},
				})
				tc.Spec.Pump.Replicas = 5
			},
			errOnUpdateCm:  true,
			errOnUpdateSvc: false,
			errOnUpdateSet: false,
			expectFn: func(g *GomegaWithT, r *result) {
				g.Expect(r.sync).NotTo(Succeed())
				g.Expect(r.svc.Spec.Ports[0].Port).NotTo(Equal(int32(8888)))
				g.Expect(r.cm.Data["pump-config"]).NotTo(ContainSubstring("stop-write-at-available-space"))
				g.Expect(*r.set.Spec.Replicas).To(Equal(int32(3)))
			},
		},
		{
			name: "error on update service",
			prepare: func(tc *v1alpha1.TidbCluster, _ *pumpFakeIndexers) {
				tc.Spec.Pump.GenericConfig = config.New(map[string]interface{}{
					"gc": 6,
					"storage": map[string]interface{}{
						"stop-write-at-available-space": "10Gi",
					},
				})
				tc.Spec.Pump.Replicas = 5
			},
			errOnUpdateCm:  false,
			errOnUpdateSvc: true,
			errOnUpdateSet: false,
			expectFn: func(g *GomegaWithT, r *result) {
				g.Expect(r.sync).NotTo(Succeed())
				g.Expect(r.svc.Spec.Ports[0].Port).To(Equal(int32(8888)))
				g.Expect(r.cm.Data["pump-config"]).NotTo(ContainSubstring("stop-write-at-available-space"))
				g.Expect(*r.set.Spec.Replicas).To(Equal(int32(3)))
			},
		},
		{
			name: "error on update statefulset",
			prepare: func(tc *v1alpha1.TidbCluster, _ *pumpFakeIndexers) {
				tc.Spec.Pump.GenericConfig = config.New(map[string]interface{}{
					"gc": 6,
					"storage": map[string]interface{}{
						"stop-write-at-available-space": "10Gi",
					},
				})
				tc.Spec.Pump.Replicas = 5
			},
			errOnUpdateCm:  false,
			errOnUpdateSvc: false,
			errOnUpdateSet: true,
			expectFn: func(g *GomegaWithT, r *result) {
				g.Expect(r.sync).NotTo(Succeed())
				g.Expect(r.svc.Spec.Ports[0].Port).NotTo(Equal(int32(8888)))
				g.Expect(r.cm.Data["pump-config"]).To(ContainSubstring("stop-write-at-available-space"))
				g.Expect(*r.set.Spec.Replicas).To(Equal(int32(3)))
			},
		},
	}

	for _, tt := range tests {
		testFn(tt, t)
	}
}

func TestSyncConfigUpdate(t *testing.T) {
	g := NewGomegaWithT(t)

	type result struct {
		sync   error
		oldSet *appsv1.StatefulSet
		set    *appsv1.StatefulSet
		getSet error
		oldCm  *corev1.ConfigMap
		cms    []*corev1.ConfigMap
		listCm error
	}
	type testcase struct {
		name     string
		prepare  func(*v1alpha1.TidbCluster, *pumpFakeIndexers)
		expectFn func(*GomegaWithT, *result)
	}

	testFn := func(test *testcase, t *testing.T) {
		t.Log(test.name)

		tc := newTidbClusterForPump()
		ns := tc.Namespace
		tcName := tc.Name
		tc.Spec.Pump.ConfigUpdateStrategy = v1alpha1.ConfigUpdateStrategyRollingUpdate

		pmm, _, indexers := newFakePumpMemberManager()

		oldCm, err := getNewPumpConfigMap(tc)
		g.Expect(err).To(Succeed())
		oldSvc := getNewPumpHeadlessService(tc)
		oldSvc.Spec.Ports[0].Port = 8888
		oldSet, err := getNewPumpStatefulSet(tc, oldCm)
		g.Expect(err).To(Succeed())

		g.Expect(indexers.set.Add(oldSet)).To(Succeed())
		g.Expect(indexers.svc.Add(oldSvc)).To(Succeed())
		g.Expect(indexers.cm.Add(oldCm)).To(Succeed())

		if test.prepare != nil {
			test.prepare(tc, indexers)
		}

		syncErr := pmm.Sync(tc)
		set, getStsErr := pmm.setLister.StatefulSets(ns).Get(controller.PumpMemberName(tcName))
		sel, err := label.New().Pump().Selector()
		g.Expect(err).To(Succeed())
		cms, listCmErr := pmm.cmLister.List(sel)
		result := result{syncErr, oldSet, set, getStsErr, oldCm, cms, listCmErr}
		test.expectFn(g, &result)
	}

	tests := []*testcase{
		{
			name: "basic",
			prepare: func(tc *v1alpha1.TidbCluster, _ *pumpFakeIndexers) {
				tc.Spec.Pump.GenericConfig = config.New(map[string]interface{}{
					"gc": 6,
					"storage": map[string]interface{}{
						"stop-write-at-available-space": "10Gi",
					},
				})
			},
			expectFn: func(g *GomegaWithT, r *result) {
				g.Expect(r.sync).To(Succeed())
				g.Expect(r.listCm).To(Succeed())
				g.Expect(r.cms).To(HaveLen(2))
				g.Expect(r.getSet).To(Succeed())
				using := FindPumpConfig("test", r.set.Spec.Template.Spec.Volumes)
				g.Expect(using).NotTo(BeEmpty())
				var usingCm *corev1.ConfigMap
				for _, cm := range r.cms {
					if cm.Name == using {
						usingCm = cm
					}
				}
				g.Expect(usingCm).NotTo(BeNil(), "The configmap used by statefulset must be created")
				g.Expect(usingCm.Data["pump-config"]).To(ContainSubstring("stop-write-at-available-space"),
					"The configmap used by statefulset should be the latest one")
			},
		},
	}

	for _, tt := range tests {
		testFn(tt, t)
	}
}

type pumpFakeIndexers struct {
	tc  cache.Indexer
	cm  cache.Indexer
	svc cache.Indexer
	set cache.Indexer
}

type pumpFakeControls struct {
	svc *controller.FakeServiceControl
	set *controller.FakeStatefulSetControl
	cm  *controller.FakeConfigMapControl
}

func newFakePumpMemberManager() (*pumpMemberManager, *pumpFakeControls, *pumpFakeIndexers) {
	cli := fake.NewSimpleClientset()
	kubeCli := kubefake.NewSimpleClientset()
	setInformer := kubeinformers.NewSharedInformerFactory(kubeCli, 0).Apps().V1().StatefulSets()
	tcInformer := informers.NewSharedInformerFactory(cli, 0).Pingcap().V1alpha1().TidbClusters()
	svcInformer := kubeinformers.NewSharedInformerFactory(kubeCli, 0).Core().V1().Services()
	epsInformer := kubeinformers.NewSharedInformerFactory(kubeCli, 0).Core().V1().Endpoints()
	cmInformer := kubeinformers.NewSharedInformerFactory(kubeCli, 0).Core().V1().ConfigMaps()
	setControl := controller.NewFakeStatefulSetControl(setInformer, tcInformer)
	svcControl := controller.NewFakeServiceControl(svcInformer, epsInformer, tcInformer)
	cmControl := controller.NewFakeConfigMapControl(cmInformer)
	pmm := &pumpMemberManager{
		setControl,
		svcControl,
		cmControl,
		setInformer.Lister(),
		svcInformer.Lister(),
		cmInformer.Lister(),
	}
	controls := &pumpFakeControls{
		svc: svcControl,
		set: setControl,
		cm:  cmControl,
	}
	indexers := &pumpFakeIndexers{
		tc:  tcInformer.Informer().GetIndexer(),
		svc: svcInformer.Informer().GetIndexer(),
		cm:  cmInformer.Informer().GetIndexer(),
		set: setInformer.Informer().GetIndexer(),
	}
	return pmm, controls, indexers
}

func newTidbClusterForPump() *v1alpha1.TidbCluster {
	return &v1alpha1.TidbCluster{
		TypeMeta: metav1.TypeMeta{
			Kind:       "TidbCluster",
			APIVersion: "pingcap.com/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: corev1.NamespaceDefault,
			UID:       types.UID("test"),
		},
		Spec: v1alpha1.TidbClusterSpec{
			PD: v1alpha1.PDSpec{
				ComponentSpec: v1alpha1.ComponentSpec{
					Image: "pd-test-image",
				},
				Replicas:         1,
				StorageClassName: "my-storage-class",
			},
			TiKV: v1alpha1.TiKVSpec{
				ComponentSpec: v1alpha1.ComponentSpec{
					Image: "tikv-test-image",
				},
				Replicas:         1,
				StorageClassName: "my-storage-class",
			},
			TiDB: v1alpha1.TiDBSpec{
				ComponentSpec: v1alpha1.ComponentSpec{
					Image: "tidb-test-image",
				},
				Replicas:         1,
				StorageClassName: "my-storage-class",
			},
			Pump: &v1alpha1.PumpSpec{
				ComponentSpec: v1alpha1.ComponentSpec{
					Image: "pump-test-image",
				},
				ConfigUpdateStrategy: v1alpha1.ConfigUpdateStrategyInPlace,
				GenericConfig: config.New(map[string]interface{}{
					"gc": 7,
				}),
				Replicas: 3,
				Resources: v1alpha1.Resources{
					Requests: &v1alpha1.ResourceRequirement{
						CPU:     "1",
						Memory:  "2Gi",
						Storage: "100Gi",
					},
				},
				StorageClassName: "my-storage-class",
			},
		},
	}
}

func TestGetNewPumpHeadlessService(t *testing.T) {
	tests := []struct {
		name     string
		tc       v1alpha1.TidbCluster
		expected corev1.Service
	}{
		{
			name: "basic",
			tc: v1alpha1.TidbCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo",
					Namespace: "ns",
				},
				Spec: v1alpha1.TidbClusterSpec{
					Pump: &v1alpha1.PumpSpec{},
				},
			},
			expected: corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo-pump",
					Namespace: "ns",
					Labels: map[string]string{
						"app.kubernetes.io/name":       "tidb-cluster",
						"app.kubernetes.io/managed-by": "tidb-operator",
						"app.kubernetes.io/instance":   "",
						"app.kubernetes.io/component":  "pump",
					},
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "pingcap.com/v1alpha1",
							Kind:       "TidbCluster",
							Name:       "foo",
							UID:        "",
							Controller: func(b bool) *bool {
								return &b
							}(true),
							BlockOwnerDeletion: func(b bool) *bool {
								return &b
							}(true),
						},
					},
				},
				Spec: corev1.ServiceSpec{
					ClusterIP: "None",
					Ports: []corev1.ServicePort{
						{
							Name:       "pump",
							Port:       8250,
							TargetPort: intstr.FromInt(8250),
							Protocol:   corev1.ProtocolTCP,
						},
					},
					Selector: map[string]string{
						"app.kubernetes.io/name":       "tidb-cluster",
						"app.kubernetes.io/managed-by": "tidb-operator",
						"app.kubernetes.io/instance":   "",
						"app.kubernetes.io/component":  "pump",
					},
					PublishNotReadyAddresses: true,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := getNewPumpHeadlessService(&tt.tc)
			if diff := cmp.Diff(tt.expected, *svc); diff != "" {
				t.Errorf("unexpected Service (-want, +got): %s", diff)
			}
		})
	}
}

func TestGetNewPumpConfigMap(t *testing.T) {
	g := NewGomegaWithT(t)

	tests := []struct {
		name     string
		tc       v1alpha1.TidbCluster
		expected corev1.ConfigMap
	}{
		{
			name: "empty config",
			tc: v1alpha1.TidbCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo",
					Namespace: "ns",
				},
				Spec: v1alpha1.TidbClusterSpec{
					Pump: &v1alpha1.PumpSpec{
						GenericConfig:        config.New(nil),
						ConfigUpdateStrategy: v1alpha1.ConfigUpdateStrategyInPlace,
					},
				},
			},
			expected: corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo-pump",
					Namespace: "ns",
					Labels: map[string]string{
						"app.kubernetes.io/name":       "tidb-cluster",
						"app.kubernetes.io/managed-by": "tidb-operator",
						"app.kubernetes.io/instance":   "",
						"app.kubernetes.io/component":  "pump",
					},
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "pingcap.com/v1alpha1",
							Kind:       "TidbCluster",
							Name:       "foo",
							UID:        "",
							Controller: func(b bool) *bool {
								return &b
							}(true),
							BlockOwnerDeletion: func(b bool) *bool {
								return &b
							}(true),
						},
					},
				},
				Data: map[string]string{
					"pump-config": "",
				},
			},
		},
		{
			name: "inplace update",
			tc: v1alpha1.TidbCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo",
					Namespace: "ns",
				},
				Spec: v1alpha1.TidbClusterSpec{
					Pump: &v1alpha1.PumpSpec{
						GenericConfig: config.New(map[string]interface{}{
							"gc": 7,
							"storage": map[string]interface{}{
								"sync-log": "true",
							},
						}),
						ConfigUpdateStrategy: v1alpha1.ConfigUpdateStrategyInPlace,
					},
				},
			},
			expected: corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo-pump",
					Namespace: "ns",
					Labels: map[string]string{
						"app.kubernetes.io/name":       "tidb-cluster",
						"app.kubernetes.io/managed-by": "tidb-operator",
						"app.kubernetes.io/instance":   "",
						"app.kubernetes.io/component":  "pump",
					},
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "pingcap.com/v1alpha1",
							Kind:       "TidbCluster",
							Name:       "foo",
							UID:        "",
							Controller: func(b bool) *bool {
								return &b
							}(true),
							BlockOwnerDeletion: func(b bool) *bool {
								return &b
							}(true),
						},
					},
				},
				Data: map[string]string{
					"pump-config": `gc = 7

[storage]
  sync-log = "true"
`,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm, err := getNewPumpConfigMap(&tt.tc)
			g.Expect(err).To(Succeed())
			if diff := cmp.Diff(tt.expected, *cm); diff != "" {
				t.Errorf("unexpected ConfigMap (-want, +got): %s", diff)
			}
		})
	}
}

// TODO: add ut for getPumpStatefulSet
