package collectors

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/model"

	"github.com/bosh-prometheus/bosh_exporter/deployments"
	"github.com/bosh-prometheus/bosh_exporter/filters"

	"k8s.io/client-go/kubernetes"
	"k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	clientset                                       *kubernetes.Clientset
	serviceDiscoveryFilename                        string
	azsFilter                                       *filters.AZsFilter
	processesFilter                                 *filters.RegexpFilter
	cidrsFilter                                     *filters.CidrFilter
	lastServiceDiscoveryScrapeTimestampMetric       prometheus.Gauge
	lastServiceDiscoveryScrapeDurationSecondsMetric prometheus.Gauge
	mu                                              *sync.Mutex
}

func NewServiceDiscoveryCollector(
	namespace string,
	environment string,
	boshName string,
	boshUUID string,
	serviceDiscoveryFilename string,
	azsFilter *filters.AZsFilter,
	processesFilter *filters.RegexpFilter,
	cidrsFilter *filters.CidrFilter,
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

	// creates the in-cluster config
	config, err := rest.InClusterConfig()
	if err != nil {
		panic(err.Error())
	}
	// creates the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	collector := &ServiceDiscoveryCollector{
		clientset:                clientset,
		serviceDiscoveryFilename: serviceDiscoveryFilename,
		azsFilter:                azsFilter,
		processesFilter:          processesFilter,
		cidrsFilter:              cidrsFilter,
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

	err := c.writeTargetGroupsToFile(targetGroups)

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
		fmt.Printf("error marshalling target groups: %v", err)
		return errors.New(fmt.Sprintf("Error while marshalling TargetGroups: %v", err))
	}

	cm, err := c.clientset.CoreV1().ConfigMaps("monitoring").Get("bosh-target-groups", metav1.GetOptions{
		TypeMeta: metav1.TypeMeta{Kind:"ConfigMap", APIVersion:"v1"},
	});
	data := make(map[string]string)
	data["bosh_target_groups.json"] = string(targetGroupsJSON)
	if err != nil {
		fmt.Printf("error getting configmap '%s' with data '%v'\n", err.Error(), cm)
		cm, err := c.clientset.CoreV1().ConfigMaps("monitoring").Create(&v1.ConfigMap{
			Data: data,
			ObjectMeta: metav1.ObjectMeta{Name: "bosh-target-groups", Namespace: "monitoring"},
		})
		if err != nil {
			fmt.Printf("error creating configmap %s with data %#v\n", err.Error(), data)
		} else {
			fmt.Printf("configmap created %#v\n", cm)
		}
		return err
	}
	fmt.Printf("size of data before update %d\n", len(data["bosh_target_groups.json"]))
	cm, err = c.clientset.CoreV1().ConfigMaps("monitoring").Update(&v1.ConfigMap{
			Data: data,
			ObjectMeta: metav1.ObjectMeta{Name: "bosh-target-groups", Namespace: "monitoring"},
		})
	if err != nil {
		fmt.Printf("error updating configmap %s with data %#v\n", err.Error(), cm.Data)
	}
	fmt.Printf("configmap updated %#v\n", cm)
	fmt.Printf("size of data after update %d\n", len(cm.Data["bosh_target_groups.json"]))

	return err
}
