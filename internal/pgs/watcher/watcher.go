package watcher

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"pg-selector/internal/k8s"
	"pg-selector/internal/logger"
	"pg-selector/internal/utils"

	"github.com/go-pg/pg/v10"
	corev1 "k8s.io/api/core/v1"
	disv1 "k8s.io/api/discovery/v1"
	errorsv1 "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	k8sResourceLabelKeyReplicationRole = "replication-role"
	k8sServiceRegexExpression          = "([a-z0-9-_]+\\.[a-z0-9-_]+)\\.svc(\\.[a-z0-9-_]+\\.[a-z0-9-_]+)*"
)

var (
	pgConnectionStringEnv = os.ExpandEnv(os.Getenv("PG_CONNECTION_STRING"))
)

type WatcherT struct {
	log logger.LoggerT

	waitTime time.Duration

	pgOps *pg.Options

	k8s k8sT
}

type k8sT struct {
	ctx context.Context
	clt *kubernetes.Clientset

	namespace string
	svcName   string
	svcPort   string
	svcCreate bool
}

type OptionsT struct {
	LogLevel string

	WaitTime         time.Duration
	ServicesCreation bool
}

func NewWatcher(ops OptionsT) (w *WatcherT, err error) {
	w = &WatcherT{
		log: logger.NewLogger(logger.GetLevel(ops.LogLevel)),

		waitTime: ops.WaitTime,
		k8s: k8sT{
			ctx:       context.Background(),
			svcCreate: ops.ServicesCreation,
		},
	}

	// TODO
	w.pgOps, err = pg.ParseURL(pgConnectionStringEnv)
	if err != nil {
		return w, err
	}

	addrPieces := strings.Split(w.pgOps.Addr, ":")
	w.k8s.svcPort = "5432"
	if len(addrPieces) > 1 {
		w.k8s.svcPort = addrPieces[1]
	}

	// TODO
	regex, err := regexp.Compile(k8sServiceRegexExpression)
	if err != nil {
		return w, err
	}

	if !regex.MatchString(addrPieces[0]) {
		err = fmt.Errorf("service host does not have the rigth format")
		return w, err
	}

	//
	svc := strings.Split(addrPieces[0], ".")
	w.k8s.svcName = svc[0]
	w.k8s.namespace = svc[1]

	w.k8s.clt, err = k8s.NewClient()
	if err != nil {
		return w, err
	}

	return w, err
}

func (w *WatcherT) Run(ctx context.Context, wg *sync.WaitGroup) {
	var logExtra logger.ExtraFieldsT = utils.GetBaseLogExtra("watcher")
	defer wg.Done()

	runWatcher := true
	for runWatcher {
		select {
		case <-ctx.Done():
			{
				runWatcher = false
				w.log.Info("execution cancelled", logExtra)
			}
		default:
			{
				w.log.Debug("wait to next sync", logExtra)
				time.Sleep(w.waitTime)

				endpointListRes, err := w.k8s.clt.DiscoveryV1().EndpointSlices(w.k8s.namespace).List(context.Background(), metav1.ListOptions{})
				if err != nil {
					logExtra.Set("error", err.Error())
					w.log.Error("impossible to get EndpointSlice resource", logExtra)
					logExtra.Del("error")
					continue
				}

				// Get EndpointSlice owned by user-defined Service in the connection string
				endpointsRes := disv1.EndpointSlice{}
				for _, currentEndpointRes := range endpointListRes.Items {
					for _, ownerReference := range currentEndpointRes.OwnerReferences {
						if ownerReference.Kind == "Service" && ownerReference.Name == w.k8s.svcName {
							endpointsRes = currentEndpointRes
						}
					}
				}

				logExtra.Set("endpointSlice", endpointsRes.Name)
				w.log.Debug("get EndpointSlice from user-defined service", logExtra)

				for _, endpoint := range endpointsRes.Endpoints {

					// No addresses, continue
					if len(endpoint.Addresses) == 0 {
						continue
					}

					w.pgOps.Addr = endpoint.Addresses[0] + ":" + w.k8s.svcPort
					//parsedConnectionString.Addr = w.k8s.svcName + "." + w.k8s.namespace + ".svc:" + addrPort // TODO: DELETE THIS

					db := pg.Connect(w.pgOps)

					logExtra.Set("pod", endpoint.TargetRef.Name)
					w.log.Debug("execute query replication role for node", logExtra)

					var isReplica bool
					_, err = db.QueryOne(pg.Scan(&isReplica), "SELECT pg_is_in_recovery()")
					if err != nil {
						logExtra.Set("error", err.Error())
						w.log.Error("unable to execute query on Postgres node", logExtra)
						logExtra.Del("error")
						break
					}

					// TODO
					err = db.Close()
					if err != nil {
						logExtra.Set("error", err.Error())
						w.log.Warn("impossible to close connection in database", logExtra)
						logExtra.Del("error")
					}

					// TODO
					w.log.Debug("retrieve pod resource", logExtra)

					podRes, err := w.k8s.clt.CoreV1().Pods(w.k8s.namespace).
						Get(w.k8s.ctx, endpoint.TargetRef.Name, metav1.GetOptions{})
					if err != nil {
						logExtra.Set("error", err.Error())
						w.log.Error("unable to get pod", logExtra)
						logExtra.Del("error")
						break
					}

					survivingLabels := map[string]string{}
					for labelKey, labelValue := range podRes.Labels {
						if labelKey != k8sResourceLabelKeyReplicationRole {
							survivingLabels[labelKey] = labelValue
						}
					}

					//
					survivingLabels[k8sResourceLabelKeyReplicationRole] = "standby"
					if !isReplica {
						survivingLabels[k8sResourceLabelKeyReplicationRole] = "primary"
					}

					podRes.SetLabels(survivingLabels)

					w.log.Debug("updating labels in pod", logExtra)

					_, err = w.k8s.clt.CoreV1().Pods(w.k8s.namespace).
						Update(w.k8s.ctx, podRes, metav1.UpdateOptions{})
					if err != nil {
						logExtra.Set("error", err.Error())
						w.log.Warn("unable to update pod", logExtra)
						logExtra.Del("error")
						continue
					}
				}
				logExtra.Del("pod")
				if err != nil {
					continue
				}

				if w.k8s.svcCreate {
					serviceRes, err := w.k8s.clt.CoreV1().Services(w.k8s.namespace).Get(w.k8s.ctx, w.k8s.svcName, metav1.GetOptions{})
					if err != nil {
						logExtra.Set("error", err.Error())
						w.log.Warn("impossible to get Service resources", logExtra)
						logExtra.Del("error")
						continue
					}

					serviceRes.Spec.Selector[k8sResourceLabelKeyReplicationRole] = "primary"
					serviceRes.Labels["app.kubernetes.io/managed-by"] = "pg-selector"
					for k := range serviceRes.Labels {
						if strings.HasPrefix(k, "helm.sh") {
							delete(serviceRes.Labels, k)
						}
					}

					newService := corev1.Service{
						ObjectMeta: metav1.ObjectMeta{
							Name:        serviceRes.Name + "-primary",
							Labels:      serviceRes.Labels,
							Annotations: map[string]string{},
						},
						Spec: corev1.ServiceSpec{
							Type:     corev1.ServiceTypeClusterIP,
							Ports:    serviceRes.Spec.Ports,
							Selector: serviceRes.Spec.Selector,
						},
					}

					_, err = w.k8s.clt.CoreV1().Services(w.k8s.namespace).Update(w.k8s.ctx, &newService, metav1.UpdateOptions{})
					if err != nil {
						if errorsv1.IsNotFound(err) {
							_, err = w.k8s.clt.CoreV1().Services(w.k8s.namespace).Create(w.k8s.ctx, &newService, metav1.CreateOptions{})
							if err != nil {
								logExtra.Set("error", err.Error())
								w.log.Error("impossible to create service", logExtra)
								logExtra.Del("error")
								continue
							}
						} else {
							logExtra.Set("error", err.Error())
							w.log.Error("impossible to update service", logExtra)
							logExtra.Del("error")
							continue
						}
					}
				}
			}
		}
	}
}
