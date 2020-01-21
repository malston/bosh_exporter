package collectors

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/model"

	"github.com/bosh-prometheus/bosh_exporter/deployments"
	"github.com/bosh-prometheus/bosh_exporter/filters"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	boshDeploymentNameLabel = model.MetaLabelPrefix + "bosh_deployment"
	boshJobProcessNameLabel = model.MetaLabelPrefix + "bosh_job_process_name"
)

type LabelGroups map[LabelGroupKey][]string

type LabelGroupKey struct {
	DeploymentName string
	ProcessName    string
}

func (k *LabelGroupKey) Labels() model.LabelSet {
	return model.LabelSet{
		model.LabelName(boshDeploymentNameLabel): model.LabelValue(k.DeploymentName),
		model.LabelName(boshJobProcessNameLabel): model.LabelValue(k.ProcessName),
	}
}

type TargetGroups []TargetGroup

type TargetGroup struct {
	Targets []string       `json:"targets"`
	Labels  model.LabelSet `json:"labels,omitempty"`
}

type ServiceDiscoveryCollector struct {
	k8sNamespace                                    string
	serviceDiscoveryFilename                        string
	serviceDiscoveryConfigMap                       string
	azsFilter                                       *filters.AZsFilter
	processesFilter                                 *filters.RegexpFilter
	cidrsFilter                                     *filters.CidrFilter
	lastServiceDiscoveryScrapeTimestampMetric       prometheus.Gauge
	lastServiceDiscoveryScrapeDurationSecondsMetric prometheus.Gauge
	clientset                                       kubernetes.Interface
	mu                                              *sync.Mutex
}

func NewServiceDiscoveryCollector(
	namespace string,
	environment string,
	boshName string,
	boshUUID string,
	k8sNamespace string,
	serviceDiscoveryFilename string,
	serviceDiscoveryConfigMap string,
	azsFilter *filters.AZsFilter,
	processesFilter *filters.RegexpFilter,
	cidrsFilter *filters.CidrFilter,
	clientset kubernetes.Interface,
) *ServiceDiscoveryCollector {
	lastServiceDiscoveryScrapeTimestampMetric := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: "",
			Name:      "last_service_discovery_scrape_timestamp",
			Help:      "Number of seconds since 1970 since last scrape of Service Discovery from BOSH.",
			ConstLabels: prometheus.Labels{
				"environment": environment,
				"bosh_name":   boshName,
				"bosh_uuid":   boshUUID,
			},
		},
	)

	lastServiceDiscoveryScrapeDurationSecondsMetric := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: "",
			Name:      "last_service_discovery_scrape_duration_seconds",
			Help:      "Duration of the last scrape of Service Discovery from BOSH.",
			ConstLabels: prometheus.Labels{
				"environment": environment,
				"bosh_name":   boshName,
				"bosh_uuid":   boshUUID,
			},
		},
	)

	collector := &ServiceDiscoveryCollector{
		clientset:                 clientset,
		k8sNamespace:              k8sNamespace,
		serviceDiscoveryFilename:  serviceDiscoveryFilename,
		serviceDiscoveryConfigMap: serviceDiscoveryConfigMap,
		azsFilter:                 azsFilter,
		processesFilter:           processesFilter,
		cidrsFilter:               cidrsFilter,
		lastServiceDiscoveryScrapeTimestampMetric:       lastServiceDiscoveryScrapeTimestampMetric,
		lastServiceDiscoveryScrapeDurationSecondsMetric: lastServiceDiscoveryScrapeDurationSecondsMetric,
		mu: &sync.Mutex{},
	}

	return collector
}

func (c *ServiceDiscoveryCollector) Collect(deployments []deployments.DeploymentInfo, ch chan<- prometheus.Metric) error {
	var begun = time.Now()

	labelGroups := c.createLabelGroups(deployments)
	targetGroups := c.createTargetGroups(labelGroups)

	var err error
	if c.clientset != nil && c.serviceDiscoveryConfigMap != "" {
		err = c.writeTargetGroupsToConfigMap(targetGroups)
	} else {
		err = c.writeTargetGroupsToFile(targetGroups)
	}
	if err != nil {
		return err
	}

	c.lastServiceDiscoveryScrapeTimestampMetric.Set(float64(time.Now().Unix()))
	c.lastServiceDiscoveryScrapeTimestampMetric.Collect(ch)

	c.lastServiceDiscoveryScrapeDurationSecondsMetric.Set(time.Since(begun).Seconds())
	c.lastServiceDiscoveryScrapeDurationSecondsMetric.Collect(ch)

	return err
}

func (c *ServiceDiscoveryCollector) Describe(ch chan<- *prometheus.Desc) {
	c.lastServiceDiscoveryScrapeTimestampMetric.Describe(ch)
	c.lastServiceDiscoveryScrapeDurationSecondsMetric.Describe(ch)
}

func (c *ServiceDiscoveryCollector) getLabelGroupKey(
	deployment deployments.DeploymentInfo,
	instance deployments.Instance,
	process deployments.Process,
) LabelGroupKey {
	return LabelGroupKey{
		DeploymentName: deployment.Name,
		ProcessName:    process.Name,
	}
}

func (c *ServiceDiscoveryCollector) createLabelGroups(deployments []deployments.DeploymentInfo) LabelGroups {
	labelGroups := LabelGroups{}

	for _, deployment := range deployments {
		for _, instance := range deployment.Instances {
			ip, found := c.cidrsFilter.Select(instance.IPs)
			if !found || !c.azsFilter.Enabled(instance.AZ) {
				continue
			}

			for _, process := range instance.Processes {
				if !c.processesFilter.Enabled(process.Name) {
					continue
				}
				key := c.getLabelGroupKey(deployment, instance, process)
				if _, ok := labelGroups[key]; !ok {
					labelGroups[key] = []string{}
				}
				labelGroups[key] = append(labelGroups[key], ip)
			}
		}
	}

	return labelGroups
}

func (c *ServiceDiscoveryCollector) createTargetGroups(labelGroups LabelGroups) TargetGroups {
	targetGroups := TargetGroups{}

	for key, targets := range labelGroups {
		targetGroups = append(targetGroups, TargetGroup{
			Labels:  key.Labels(),
			Targets: targets,
		})
	}

	return targetGroups
}

func (c *ServiceDiscoveryCollector) writeTargetGroupsToFile(targetGroups TargetGroups) error {
	targetGroupsJSON, err := json.Marshal(targetGroups)
	if err != nil {
		return errors.New(fmt.Sprintf("Error while marshalling TargetGroups: %v", err))
	}

	dir, name := path.Split(c.serviceDiscoveryFilename)
	f, err := ioutil.TempFile(dir, name)
	if err != nil {
		return errors.New(fmt.Sprintf("Error creating temp file: %v", err))
	}

	_, err = f.Write(targetGroupsJSON)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if permErr := os.Chmod(f.Name(), 0644); err == nil {
		err = permErr
	}
	if err == nil {
		err = os.Rename(f.Name(), c.serviceDiscoveryFilename)
	}

	if err != nil {
		os.Remove(f.Name())
	}

	return err
}

func (c *ServiceDiscoveryCollector) writeTargetGroupsToConfigMap(targetGroups TargetGroups) error {
	targetGroupsJSON, err := json.Marshal(targetGroups)
	if err != nil {
		return fmt.Errorf("Error while marshalling TargetGroups: %v", err)
	}
	data := map[string]string{
		c.serviceDiscoveryFilename: string(targetGroupsJSON),
	}

	_, err = c.clientset.CoreV1().ConfigMaps(c.k8sNamespace).Get(c.serviceDiscoveryConfigMap, metav1.GetOptions{
		TypeMeta: metav1.TypeMeta{Kind: "ConfigMap", APIVersion: "v1"},
	})
	if err != nil {
		_, err := c.clientset.CoreV1().ConfigMaps(c.k8sNamespace).Create(&v1.ConfigMap{
			Data:       data,
			ObjectMeta: metav1.ObjectMeta{Name: c.serviceDiscoveryConfigMap},
		})
		if err != nil {
			return fmt.Errorf("error creating configmap: %v", err)
		}
		return err
	}
	_, err = c.clientset.CoreV1().ConfigMaps(c.k8sNamespace).Update(&v1.ConfigMap{
		Data:       data,
		ObjectMeta: metav1.ObjectMeta{Name: c.serviceDiscoveryConfigMap},
	})
	if err != nil {
		return fmt.Errorf("error updating configmap: %v", err)
	}

	return err
}
