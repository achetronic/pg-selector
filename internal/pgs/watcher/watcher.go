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
	"k8s.io/client-go/kubernetes"
)

const (
	k8sServiceRegexExpression = "([a-z0-9-_]+\\.[a-z0-9-_]+)\\.svc(\\.[a-z0-9-_]+\\.[a-z0-9-_]+)*"
)

var (
	pgConnectionStringEnv = os.ExpandEnv(os.Getenv("PG_CONNECTION_STRING"))
)

type WatcherT struct {
	ctx context.Context
	log logger.LoggerT

	waitTime time.Duration

	pgOps *pg.Options

	k8s k8sT
}

type k8sT struct {
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
		ctx: context.Background(),
		log: logger.NewLogger(logger.GetLevel(ops.LogLevel)),

		waitTime: ops.WaitTime,
		k8s: k8sT{
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
	var err error
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
				logExtra.Del("error")
				logExtra.Del("pgPodsInfo")

				w.log.Debug("wait to next sync", logExtra)
				time.Sleep(w.waitTime)

				var pgPodsInfo pgPodsInfoT
				pgPodsInfo, err = w.getPGPodsInfo()
				if err != nil {
					logExtra.Set("error", err.Error())
					w.log.Error("unable to get pg pods info", logExtra)
					continue
				}
				logExtra.Set("pgPodsInfo", pgPodsInfo)
				w.log.Debug("success getting pg pods info", logExtra)

				err = w.setPGPodsType(&pgPodsInfo)
				if err != nil {
					logExtra.Set("error", err.Error())
					w.log.Error("unable to set pg pods types", logExtra)
					continue
				}
				logExtra.Set("pgPodsInfo", pgPodsInfo)
				w.log.Debug("success setting pg pods types", logExtra)

				err = w.patchPGPodsLabels(&pgPodsInfo)
				if err != nil {
					logExtra.Set("error", err.Error())
					w.log.Error("unable to update pg pods labels", logExtra)
					continue
				}
				w.log.Debug("success updating pg pods labels", logExtra)

				//

				if w.k8s.svcCreate {
					err = w.createPGPodsServices()
					if err != nil {
						logExtra.Set("error", err.Error())
						w.log.Error("unable to create pg pods services", logExtra)
						continue
					}
					w.log.Debug("success creating pg pods services", logExtra)
				}
			}

			w.log.Info("success in flow execution", logExtra)
		}
	}
}
