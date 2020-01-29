package filters_test

import (
	"errors"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/cloudfoundry/bosh-cli/director"
	"github.com/cloudfoundry/bosh-cli/director/directorfakes"
	"github.com/prometheus/common/log"

	. "github.com/bosh-prometheus/bosh_exporter/filters"
)

func init() {
	log.Base().SetLevel("fatal")
}

var _ = Describe("DeploymentsFilter", func() {
	var (
		err               error
		filters           []string
		boshClient        *directorfakes.FakeDirector
		deploymentsFilter *DeploymentsFilter
		queuedTaskLimit   int
	)

	Describe("GetDeployments", func() {
		var (
			deployment1    director.Deployment
			deployment2    director.Deployment
			allDeployments []director.Deployment

			deployments []director.Deployment
			tasks       []director.Task
		)

		BeforeEach(func() {
			filters = []string{}
			boshClient = &directorfakes.FakeDirector{}
			queuedTaskLimit = 0

			deployment1 = &directorfakes.FakeDeployment{
				NameStub: func() string { return "fake-deployment-name-1" },
			}
			deployment2 = &directorfakes.FakeDeployment{
				NameStub: func() string { return "fake-deployment-name-2" },
			}
			allDeployments = []director.Deployment{}
			tasks = []director.Task{&directorfakes.FakeTask{}, &directorfakes.FakeTask{}}
		})

		JustBeforeEach(func() {
			deploymentsFilter = NewDeploymentsFilter(filters, boshClient, queuedTaskLimit)
			deployments, err = deploymentsFilter.GetDeployments()
		})

		Context("when there are more than max queued tasks", func() {
			BeforeEach(func() {
				queuedTaskLimit = 1
				boshClient.CurrentTasksReturns(tasks, nil)
			})

			It("returns nil deployments", func() {
				Expect(deployments).To(BeNil())
				Expect(err).ToNot(HaveOccurred())
			})
		})

		Context("when there are no filters", func() {
			BeforeEach(func() {
				boshClient.DeploymentsReturns(allDeployments, nil)
			})

			It("returns all deployments", func() {
				Expect(deployments).To(Equal(allDeployments))
				Expect(err).ToNot(HaveOccurred())
			})

			Context("and there are no deployments", func() {
				BeforeEach(func() {
					boshClient.DeploymentsReturns([]director.Deployment{}, nil)
				})

				It("does not return any deployment", func() {
					Expect(deployments).To(BeEmpty())
					Expect(err).ToNot(HaveOccurred())
				})
			})

			Context("and it fails to get the deployments", func() {
				BeforeEach(func() {
					boshClient.DeploymentsReturns(nil, errors.New("no deployments"))
				})

				It("does not return any deployment", func() {
					Expect(deployments).To(BeEmpty())
					Expect(err).To(HaveOccurred())
				})
			})
		})

		Context("when there are filters", func() {
			BeforeEach(func() {
				filters = []string{"fake-deployment-name-1"}
				boshClient.FindDeploymentReturns(deployment1, nil)
			})

			It("returns the filtered deployments", func() {
				Expect(boshClient.FindDeploymentArgsForCall(0)).To(Equal("fake-deployment-name-1"))
				Expect(deployments).To(ContainElement(deployment1))
				Expect(deployments).ToNot(ContainElement(deployment2))
				Expect(err).ToNot(HaveOccurred())
			})

			Context("and it fails to get the deployment", func() {
				BeforeEach(func() {
					boshClient.FindDeploymentReturns(nil, errors.New("deployment does not exists"))
				})

				It("does not return any deployment", func() {
					Expect(deployments).To(BeEmpty())
					Expect(err).To(HaveOccurred())
				})
			})

			Context("and the deployment name has leading and/or trailing whitespaces", func() {
				BeforeEach(func() {
					filters = []string{"   fake-deployment-name-1  "}
				})

				It("returns the filtered deployments", func() {
					Expect(boshClient.FindDeploymentArgsForCall(0)).To(Equal("fake-deployment-name-1"))
					Expect(deployments).To(ContainElement(deployment1))
					Expect(deployments).ToNot(ContainElement(deployment2))
					Expect(err).ToNot(HaveOccurred())
				})
			})
		})
	})
})
