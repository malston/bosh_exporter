package filters

import (
	"errors"
	"fmt"
	"strings"

	"github.com/cloudfoundry/bosh-cli/director"
	"github.com/prometheus/common/log"
)

type DeploymentsFilter struct {
	filters         []string
	boshClient      director.Director
	queuedTaskLimit int
}

func NewDeploymentsFilter(filters []string, boshClient director.Director, queuedTaskLimit int) *DeploymentsFilter {
	return &DeploymentsFilter{filters: filters, boshClient: boshClient, queuedTaskLimit: queuedTaskLimit}
}

func (f *DeploymentsFilter) GetDeployments() ([]director.Deployment, error) {
	var err error
	var deployments []director.Deployment

	currentQueuedTasks, err := f.boshClient.CurrentTasks(director.TasksFilter{
		All:    true,
		States: []string{"queued"},
	})

	if f.queuedTaskLimit != 0 {
		log.Debugf("Queue task limit set to `%d`, current task queue is `%d`", len(currentQueuedTasks), f.queuedTaskLimit)
		if len(currentQueuedTasks) > f.queuedTaskLimit {
			log.Debug("Queued tasks has reached the limit")
			return deployments, nil
		}
	}

	if len(f.filters) > 0 {
		log.Debugf("Filtering deployments by `%v`...", f.filters)
		for _, deploymentName := range f.filters {
			deployment, err := f.boshClient.FindDeployment(strings.Trim(deploymentName, " "))
			if err != nil {
				return deployments, errors.New(fmt.Sprintf("Error while reading deployment `%s`: %v", deploymentName, err))
			}
			deployments = append(deployments, deployment)
		}
	} else {
		log.Debugf("Reading deployments...")
		deployments, err = f.boshClient.Deployments()
		if err != nil {
			return deployments, errors.New(fmt.Sprintf("Error while reading deployments: %v", err))
		}
	}

	return deployments, nil
}
