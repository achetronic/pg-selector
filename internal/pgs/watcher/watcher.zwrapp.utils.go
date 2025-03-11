package watcher

import (
	"fmt"
	"strings"

	"github.com/go-pg/pg/v10"
	corev1 "k8s.io/api/core/v1"
	disv1 "k8s.io/api/discovery/v1"
	errorsv1 "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	k8sLabelKeyPGSReplicationRole = "pg-selector/replication-role"
	k8sLabelKeyAppManagedBy       = "app.kubernetes.io/managed-by"

	pgReplicationRoleStandby = "standby"
	pgReplicationRolePrimary = "primary"
)

type pgPodsInfoT struct {
	EsName string   `json:"endpointSliceName"`
	Pods   []pgPodT `json:"pgPods"`
}

type pgPodT struct {
	PGtype string   `json:"type"`
	Name   string   `json:"name"`
	IPs    []string `json:"ips"`
}

func (w *WatcherT) getPGPodsInfo() (info pgPodsInfoT, err error) {
	endpointListRes, err := w.k8s.clt.DiscoveryV1().EndpointSlices(w.k8s.namespace).List(w.ctx, metav1.ListOptions{})
	if err != nil {
		err = fmt.Errorf("error in get EndpointSlices list: %w", err)
		return info, err
	}

	// Get EndpointSlice owned by user-defined Service in the connection string
	endpointsRes := disv1.EndpointSlice{}
	found := false
	for _, currentEndpointRes := range endpointListRes.Items {
		for _, ownerReference := range currentEndpointRes.OwnerReferences {
			if ownerReference.Kind == "Service" && ownerReference.Name == w.k8s.svcName {
				found = true
				info.EsName = currentEndpointRes.Name
				endpointsRes = currentEndpointRes
			}
		}
	}
	if !found {
		err = fmt.Errorf("EndpointSlice in '%s' namespace with '%s' owner reference NOT found", w.k8s.namespace, w.k8s.svcName)
		return info, err
	}

	for endi := range endpointsRes.Endpoints {
		pgPod := pgPodT{
			Name: endpointsRes.Endpoints[endi].TargetRef.Name,
		}

		pgPod.IPs = append(pgPod.IPs, endpointsRes.Endpoints[endi].Addresses...)
		if len(pgPod.IPs) == 0 {
			err = fmt.Errorf("EndpointSlice in '%s' namespace with '%s' owner reference pods without addresses", w.k8s.namespace, w.k8s.svcName)
			return info, err
		}

		info.Pods = append(info.Pods, pgPod)
	}

	if len(info.Pods) == 0 {
		err = fmt.Errorf("EndpointSlice in '%s' namespace with '%s' owner reference without endpoints", w.k8s.namespace, w.k8s.svcName)
	}

	return info, err
}

func (w *WatcherT) setPGPodsType(info *pgPodsInfoT) (err error) {
	for podi := range info.Pods {

		w.pgOps.Addr = info.Pods[podi].IPs[0] + ":" + w.k8s.svcPort
		//parsedConnectionString.Addr = w.k8s.svcName + "." + w.k8s.namespace + ".svc:" + addrPort // TODO: DELETE THIS

		db := pg.Connect(w.pgOps)

		var isReplica bool
		_, err = db.QueryOne(pg.Scan(&isReplica), "SELECT pg_is_in_recovery()")
		if err != nil {
			err = fmt.Errorf("error executing query on '%s' Postgres node: %w", info.Pods[podi].Name, err)
			return err
		}

		// TODO
		err = db.Close()
		if err != nil {
			err = fmt.Errorf("error closing connection on '%s' Postgres node: %w", info.Pods[podi].Name, err)
			return err
		}

		info.Pods[podi].PGtype = pgReplicationRoleStandby
		if !isReplica {
			info.Pods[podi].PGtype = pgReplicationRolePrimary
		}
	}

	primaryNodesCount := 0
	standbyNodesCount := 0
	for podi := range info.Pods {
		if info.Pods[podi].PGtype == pgReplicationRoleStandby {
			standbyNodesCount++
		}
		if info.Pods[podi].PGtype == pgReplicationRolePrimary {
			primaryNodesCount++
		}
	}

	if primaryNodesCount != 1 {
		err = fmt.Errorf("got '%d' primary nodes in count and must be one, unable to determine the primary node", primaryNodesCount)
		return err
	}

	if len(info.Pods) != primaryNodesCount+standbyNodesCount {
		err = fmt.Errorf("mismatch between pods and primary/standby nodes count sum")
	}

	return err
}

func (w *WatcherT) patchPGPodsLabels(info *pgPodsInfoT) (err error) {
	for podi := range info.Pods {
		var podRes *corev1.Pod
		podRes, err = w.k8s.clt.CoreV1().Pods(w.k8s.namespace).Get(w.ctx, info.Pods[podi].Name, metav1.GetOptions{})
		if err != nil {
			err = fmt.Errorf("error getting '%s' pod: %w", info.Pods[podi].Name, err)
			return err
		}

		if podRes.Labels == nil {
			podRes.Labels = make(map[string]string)
		}
		podRes.Labels[k8sLabelKeyPGSReplicationRole] = info.Pods[podi].PGtype

		_, err = w.k8s.clt.CoreV1().Pods(w.k8s.namespace).Update(w.ctx, podRes, metav1.UpdateOptions{})
		if err != nil {
			err = fmt.Errorf("error updating '%s' pod labels: %w", info.Pods[podi].Name, err)
			return err
		}
	}

	return err
}

func (w *WatcherT) createPGPodsServices() (err error) {
	// get pg service reference
	var svcRes *corev1.Service
	svcRes, err = w.k8s.clt.CoreV1().Services(w.k8s.namespace).Get(w.ctx, w.k8s.svcName, metav1.GetOptions{})
	if err != nil {
		err = fmt.Errorf("error getting '%s' pg service: %w", w.k8s.svcName, err)
		return err
	}

	for k := range svcRes.Labels {
		if strings.HasPrefix(k, "helm.sh") {
			delete(svcRes.Labels, k)
		}
	}
	svcRes.Labels[k8sLabelKeyAppManagedBy] = "pg-selector"

	// create pg primary service
	primaryService := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        strings.Join([]string{svcRes.Name, pgReplicationRolePrimary}, "-"),
			Labels:      svcRes.Labels,
			Annotations: map[string]string{},
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Ports:    svcRes.Spec.Ports,
			Selector: svcRes.Spec.Selector,
		},
	}
	primaryService.Spec.Selector[k8sLabelKeyPGSReplicationRole] = pgReplicationRolePrimary

	_, err = w.k8s.clt.CoreV1().Services(w.k8s.namespace).Update(w.ctx, &primaryService, metav1.UpdateOptions{})
	if err != nil {
		if errorsv1.IsNotFound(err) {
			_, err = w.k8s.clt.CoreV1().Services(w.k8s.namespace).Create(w.ctx, &primaryService, metav1.CreateOptions{})
			if err != nil {
				err = fmt.Errorf("error creating '%s' primary service: %w", primaryService.Name, err)
				return err
			}
		} else {
			err = fmt.Errorf("error updating '%s' primary service: %w", primaryService.Name, err)
			return err
		}
	}

	// create pg standby service
	standbyService := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        strings.Join([]string{svcRes.Name, pgReplicationRoleStandby}, "-"),
			Labels:      svcRes.Labels,
			Annotations: map[string]string{},
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Ports:    svcRes.Spec.Ports,
			Selector: svcRes.Spec.Selector,
		},
	}
	standbyService.Spec.Selector[k8sLabelKeyPGSReplicationRole] = pgReplicationRoleStandby

	_, err = w.k8s.clt.CoreV1().Services(w.k8s.namespace).Update(w.ctx, &standbyService, metav1.UpdateOptions{})
	if err != nil {
		if errorsv1.IsNotFound(err) {
			_, err = w.k8s.clt.CoreV1().Services(w.k8s.namespace).Create(w.ctx, &standbyService, metav1.CreateOptions{})
			if err != nil {
				err = fmt.Errorf("error creating '%s' standby service: %w", standbyService.Name, err)
				return err
			}
		} else {
			err = fmt.Errorf("error updating '%s' standby service: %w", standbyService.Name, err)
			return err
		}
	}

	return err
}
