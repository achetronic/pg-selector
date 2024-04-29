package run

import (
	"log"
	"os"
	"regexp"
	"strings"
	"time"

	//
	"github.com/go-pg/pg/v10"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/discovery/v1"
	errorsv1 "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	//
	"pg-selector/internal/globals"
	"pg-selector/internal/kubernetes"
)

const (
	descriptionShort = `Execute replication-role labels synchronizer`
	descriptionLong  = `
	Run execute replication-role labels synchronizer`

	ReplicationLabelKey = "replication-role"
	RegexExpression     = "([a-z0-9-_]+\\.[a-z0-9-_]+)\\.svc(\\.[a-z0-9-_]+\\.[a-z0-9-_]+)*"

	//
	LogLevelFlagErrorMessage                = "impossible to get flag --log-level: %s"
	DisableTraceFlagErrorMessage            = "impossible to get flag --disable-trace: %s"
	SyncTimeFlagErrorMessage                = "impossible to get flag --sync-time: %s"
	KubernetesClientErrorMessage            = "impossible to create Kubernetes client: %s"
	HostServiceFormatErrorMessage           = "service host does not have the rigth format"
	RegexCompileErrorMessage                = "impossible to compile regex expression: %s"
	ConnectionUrlErrorMessage               = "impossible to parse postgres connection url: %s"
	UnableUpdatePodErrorMessage             = "unable to update pod: %s"
	UnableGetPodErrorMessage                = "unable to get pod: %s"
	UnableParseDurationErrorMessage         = "unable to parse duration: %s"
	UnableToExecuteQueryErrorMessage        = "unable to execute query on Postgres node: %s"
	DisableServicesCreationFlagErrorMessage = "impossible to get flag --disable-services-creation: %s"
	ServiceUpdateErrorMessage               = "impossible to update service: %s"
	ServiceCreateErrorMessage               = "impossible to create service: %s"
	DatabaseCloseErrorMessage               = "impossible to close connection to database: %s"
	EndpointSliceListErrorMessage           = "impossible to get EndpointSlice resource: %s"
	GetServiceErrorMessage                  = "impossible to get Service resources: %s"

	//
	DiscoveryEndpointsMessage    = "discovery endpoints for the host defined in connection string"
	QueryReplicationRoleMessage  = "query replication role for current node '%s' to Postgres"
	RetrievePodMessage           = "retrieve '%s' pod resource"
	PodLabelsUpdateMessage       = "updating labels in pod '%s'"
	SelectedEndpointSliceMessage = "Selected EndpointSlice from user-defined Service is: %s"
)

var (
	pgConnectionStringEnv = os.ExpandEnv(os.Getenv("PG_CONNECTION_STRING"))
)

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:                   "run",
		DisableFlagsInUseLine: true,
		Short:                 descriptionShort,
		Long:                  descriptionLong,

		Run: RunCommand,
	}

	//
	cmd.Flags().String("log-level", "info", "Verbosity level for logs")
	cmd.Flags().Bool("disable-trace", false, "Disable showing traces in logs")
	cmd.Flags().Bool("disable-services-creation", false, "Disable the creation of the services")
	cmd.Flags().String("sync-time", "5s", "Synchronization time in seconds")

	return cmd
}

// RunCommand TODO
func RunCommand(cmd *cobra.Command, args []string) {

	// Init the logger
	logLevelFlag, err := cmd.Flags().GetString("log-level")
	if err != nil {
		log.Fatalf(LogLevelFlagErrorMessage, err)
	}

	disableTraceFlag, err := cmd.Flags().GetBool("disable-trace")
	if err != nil {
		log.Fatalf(DisableTraceFlagErrorMessage, err)
	}

	err = globals.SetLogger(logLevelFlag, disableTraceFlag)
	if err != nil {
		log.Fatal(err)
	}

	syncTime, err := cmd.Flags().GetString("sync-time")
	if err != nil {
		globals.ExecContext.Logger.Fatalf(SyncTimeFlagErrorMessage, err)
	}

	disableServicesCreationFlag, err := cmd.Flags().GetBool("disable-services-creation")
	if err != nil {
		log.Fatalf(DisableServicesCreationFlagErrorMessage, err)
	}

	kubeClient, err := kubernetes.NewClient()
	if err != nil {
		globals.ExecContext.Logger.Fatalf(KubernetesClientErrorMessage, err)
	}

	// TODO
	parsedConnectionString, err := pg.ParseURL(pgConnectionStringEnv)
	if err != nil {
		globals.ExecContext.Logger.Fatalf(ConnectionUrlErrorMessage, err)
	}

	addrPieces := strings.Split(parsedConnectionString.Addr, ":")
	addrPort := "5432"
	if len(addrPieces) > 1 {
		addrPort = addrPieces[1]
	}

	// TODO
	regex, err := regexp.Compile(RegexExpression)
	if err != nil {
		globals.ExecContext.Logger.Fatalf(RegexCompileErrorMessage, err)
	}

	if !regex.MatchString(addrPieces[0]) {
		globals.ExecContext.Logger.Fatalf(HostServiceFormatErrorMessage)
	}

	//
	svc := strings.Split(addrPieces[0], ".")

	// TODO
	for {
		globals.ExecContext.Logger.Info(DiscoveryEndpointsMessage)

		endpointListRes, err := kubeClient.DiscoveryV1().EndpointSlices(svc[1]).List(globals.ExecContext.Context, metav1.ListOptions{})
		if err != nil {
			globals.ExecContext.Logger.Fatalf(EndpointSliceListErrorMessage, err)
		}

		// Get EndpointSlice owned by user-defined Service in the connection string
		endpointsRes := v1.EndpointSlice{}
		for _, currentEndpointRes := range endpointListRes.Items {
			for _, ownerReference := range currentEndpointRes.OwnerReferences {
				if ownerReference.Kind == "Service" && ownerReference.Name == svc[0] {
					endpointsRes = currentEndpointRes
				}
			}
		}

		globals.ExecContext.Logger.Infof(SelectedEndpointSliceMessage, endpointsRes.Name)

		for _, endpoint := range endpointsRes.Endpoints {

			// No addresses, continue
			if len(endpoint.Addresses) == 0 {
				continue
			}

			parsedConnectionString.Addr = endpoint.Addresses[0] + ":" + addrPort
			//parsedConnectionString.Addr = svc[0] + "." + svc[1] + ".svc:" + addrPort // TODO: DELETE THIS

			db := pg.Connect(parsedConnectionString)

			globals.ExecContext.Logger.Infof(QueryReplicationRoleMessage, endpoint.TargetRef.Name)
			var isReplica bool
			_, err = db.QueryOne(pg.Scan(&isReplica), "SELECT pg_is_in_recovery()")
			if err != nil {
				globals.ExecContext.Logger.Fatalf(UnableToExecuteQueryErrorMessage, err)
			}

			// TODO
			err = db.Close()
			if err != nil {
				globals.ExecContext.Logger.Warnf(DatabaseCloseErrorMessage, err)
			}

			// TODO
			globals.ExecContext.Logger.Infof(RetrievePodMessage, endpoint.TargetRef.Name)
			podRes, err := kubeClient.CoreV1().Pods(svc[1]).
				Get(globals.ExecContext.Context, endpoint.TargetRef.Name, metav1.GetOptions{})
			if err != nil {
				globals.ExecContext.Logger.Fatalf(UnableGetPodErrorMessage, err)
			}

			survivingLabels := map[string]string{}
			for labelKey, labelValue := range podRes.Labels {
				if labelKey != ReplicationLabelKey {
					survivingLabels[labelKey] = labelValue
				}
			}

			//
			survivingLabels[ReplicationLabelKey] = "standby"
			if !isReplica {
				survivingLabels[ReplicationLabelKey] = "primary"
			}

			podRes.SetLabels(survivingLabels)

			globals.ExecContext.Logger.Infof(PodLabelsUpdateMessage, endpoint.TargetRef.Name)

			_, err = kubeClient.CoreV1().Pods(svc[1]).
				Update(globals.ExecContext.Context, podRes, metav1.UpdateOptions{})
			if err != nil {
				globals.ExecContext.Logger.Warnf(UnableUpdatePodErrorMessage, err)
				goto waitNextLoop
			}
		}

		if !disableServicesCreationFlag {
			serviceRes, err := kubeClient.CoreV1().Services(svc[1]).Get(globals.ExecContext.Context, svc[0], metav1.GetOptions{})
			if err != nil {
				globals.ExecContext.Logger.Warnf(GetServiceErrorMessage, err)
				goto waitNextLoop
			}

			serviceRes.Spec.Selector[ReplicationLabelKey] = "primary"
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

			_, err = kubeClient.CoreV1().Services(svc[1]).Update(globals.ExecContext.Context, &newService, metav1.UpdateOptions{})
			if err != nil {
				if errorsv1.IsNotFound(err) {
					_, err = kubeClient.CoreV1().Services(svc[1]).Create(globals.ExecContext.Context, &newService, metav1.CreateOptions{})
					if err != nil {
						globals.ExecContext.Logger.Warnf(ServiceCreateErrorMessage, err)
						goto waitNextLoop
					}
				} else {
					globals.ExecContext.Logger.Warnf(ServiceUpdateErrorMessage, err)
					goto waitNextLoop
				}
			}
		}

	waitNextLoop:
		duration, err := time.ParseDuration(syncTime)
		if err != nil {
			globals.ExecContext.Logger.Fatalf(UnableParseDurationErrorMessage, err)
		}
		time.Sleep(duration)
	}
}
