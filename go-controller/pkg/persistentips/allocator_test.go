package persistentips

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"

	"github.com/containernetworking/cni/pkg/types"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	ipamclaimsapi "github.com/k8snetworkplumbingwg/ipamclaims/pkg/crd/ipamclaims/v1alpha1"
	fakeipamclaimclient "github.com/k8snetworkplumbingwg/ipamclaims/pkg/crd/ipamclaims/v1alpha1/apis/clientset/versioned/fake"
	ipamclaimsfactory "github.com/k8snetworkplumbingwg/ipamclaims/pkg/crd/ipamclaims/v1alpha1/apis/informers/externalversions"
	ipamclaimslister "github.com/k8snetworkplumbingwg/ipamclaims/pkg/crd/ipamclaims/v1alpha1/apis/listers/ipamclaims/v1alpha1"
	nadapi "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"

	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/allocator/ip/subnet"
	ovncnitypes "github.com/ovn-org/ovn-kubernetes/go-controller/pkg/cni/types"
	ovnkclient "github.com/ovn-org/ovn-kubernetes/go-controller/pkg/kube"
	ovntest "github.com/ovn-org/ovn-kubernetes/go-controller/pkg/testing"
	ovnktypes "github.com/ovn-org/ovn-kubernetes/go-controller/pkg/types"
	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/util"
)

func TestPersistenIPAllocator(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Persistent IP allocator Suite")
}

var _ = Describe("Persistent IP allocator operations", func() {
	const (
		claimName   = "claim1"
		namespace   = "ns1"
		networkName = "justanetwork"
		subnetName  = "dummy-net"
	)

	var (
		ipamClaimsReconciler *IPAMClaimReconciler
		ovnkapiclient        *ovnkclient.KubeOVN
	)

	Context("an existing, but empty IPAMClaim", func() {
		var (
			namedAllocator subnet.NamedAllocator
			netInfo        util.NetInfo
		)

		BeforeEach(func() {
			netConf := &ovncnitypes.NetConf{
				NetConf:  types.NetConf{Name: networkName},
				Topology: ovnktypes.Layer2Topology,
				Subnets:  "192.10.10.0/24",
			}
			var err error
			netInfo, err = util.NewNetInfo(netConf)
			Expect(err).NotTo(HaveOccurred())
			ovnkapiclient = &ovnkclient.KubeOVN{
				Kube: ovnkclient.Kube{},
				IPAMClaimsClient: fakeipamclaimclient.NewSimpleClientset(
					emptyDummyIPAMClaim(namespace, claimName, networkName),
				),
			}

			ipAllocator := subnet.NewAllocator()
			Expect(ipAllocator.AddOrUpdateSubnet(subnetName, ovntest.MustParseIPNets("192.168.200.0/24", "fd10::/64"))).To(Succeed())
			namedAllocator = ipAllocator.ForSubnet(subnetName)
			ipamClaimsReconciler = NewIPAMClaimReconciler(ovnkapiclient, netInfo, nil)
			Expect(ipAllocator.AddOrUpdateSubnet(subnetName, ovntest.MustParseIPNets("192.168.200.0/24", "fd10::/64"))).To(Succeed())
		})

		It("nothing to do when reconciling nil IPAMClaims", func() {
			Expect(ipamClaimsReconciler.Reconcile(nil, nil, namedAllocator)).To(Succeed())
		})

		table.DescribeTable("reconciling IPAMClaims is successful when provided with", func(oldIPAMClaim, newIPAMClaim *ipamclaimsapi.IPAMClaim) {
			Expect(ipamClaimsReconciler.Reconcile(oldIPAMClaim, newIPAMClaim, namedAllocator)).To(Succeed())
			updatedIPAMClaim, err := ovnkapiclient.IPAMClaimsClient.K8sV1alpha1().IPAMClaims(namespace).Get(context.Background(), claimName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(updatedIPAMClaim.Status.IPs).To(ConsistOf(newIPAMClaim.Status.IPs))
		},
			table.Entry(
				"no IP addresses to persist",
				nil,
				emptyDummyIPAMClaim(namespace, claimName, networkName),
			),
			table.Entry(
				"no IP addresses to persist, but it is nothing new",
				emptyDummyIPAMClaim(namespace, claimName, networkName),
				emptyDummyIPAMClaim(namespace, claimName, networkName),
			),
			table.Entry(
				"an IP addresses to persist",
				nil,
				ipamClaimWithIPs(namespace, claimName, networkName),
			),
			table.Entry(
				"an IP addresses to persist, but already present",
				ipamClaimWithIPs(namespace, claimName, networkName),
				ipamClaimWithIPs(namespace, claimName, networkName),
			),
		)
	})

	When("reconciling an IPAMClaim already featuring IPs", func() {
		const originalIPAMClaimIP = "192.168.200.2/24"

		var (
			namedAllocator subnet.NamedAllocator
			netInfo        util.NetInfo
			originalClaims []*ipamclaimsapi.IPAMClaim
		)

		BeforeEach(func() {
			netConf := &ovncnitypes.NetConf{
				NetConf:  types.NetConf{Name: networkName},
				Topology: ovnktypes.Layer2Topology,
				Subnets:  "192.10.10.0/24",
			}

			var err error
			netInfo, err = util.NewNetInfo(netConf)
			Expect(err).NotTo(HaveOccurred())

			originalClaims = []*ipamclaimsapi.IPAMClaim{
				ipamClaimWithIPs(namespace, claimName, networkName, originalIPAMClaimIP),
			}
			ipAllocator := subnet.NewAllocator()
			ovnkapiclient = &ovnkclient.KubeOVN{
				Kube: ovnkclient.Kube{},
				IPAMClaimsClient: fakeipamclaimclient.NewSimpleClientset(
					toRuntimeObj(originalClaims)...,
				),
			}
			Expect(ipAllocator.AddOrUpdateSubnet(subnetName, ovntest.MustParseIPNets("192.168.200.0/24", "fd10::/64"))).To(Succeed())
			namedAllocator = ipAllocator.ForSubnet(subnetName)
			ipamClaimsReconciler = NewIPAMClaimReconciler(ovnkapiclient, netInfo, nil)
		})

		It("the IPAMClaim is *not* updated", func() {
			originalNonEmptyClaim := originalClaims[0]
			Expect(ipamClaimsReconciler.Reconcile(
				originalNonEmptyClaim,
				ipamClaimWithIPs(namespace, claimName, networkName, originalIPAMClaimIP, "fd10::2/64"),
				namedAllocator,
			)).To(
				MatchError(
					"failed to update IPAMClaim \"ns1/claim1\" - overwriting existing IPs [\"192.168.200.2/24\"] with newer IPs [\"192.168.200.2/24\" \"fd10::2/64\"]"))
		})
	})

	Context("retrieving IPAMClaims", func() {
		table.DescribeTable(
			"succeeds",
			func(
				netConf *ovncnitypes.NetConf,
				network *nadapi.NetworkSelectionElement,
				inputClaims *ipamclaimsapi.IPAMClaim,
				expectedClaim *ipamclaimsapi.IPAMClaim,
			) {
				ctx, cancel := context.WithCancel(context.Background())
				lister, listerTeardown := generateIPAMClaimsListerAndTeardownFunc(ctx.Done(), inputClaims)
				defer func() {
					cancel()
					listerTeardown()
				}()

				netInfo, err := util.NewNetInfo(netConf)
				Expect(err).NotTo(HaveOccurred())
				Expect(
					NewIPAMClaimReconciler(nil, netInfo, lister).FindIPAMClaim(
						network.IPAMClaimReference,
						network.Namespace,
					),
				).To(Equal(expectedClaim))
			},
			table.Entry(
				"when the claim we're looking for is actually passed in layer2 topology",
				&ovncnitypes.NetConf{Topology: ovnktypes.Layer2Topology, Subnets: "192.10.10.0/24"},
				&nadapi.NetworkSelectionElement{IPAMClaimReference: claimName, Namespace: namespace},
				ipamClaimWithIPs(namespace, claimName, "192.10.10.10/24"),
				ipamClaimWithIPs(namespace, claimName, "192.10.10.10/24"),
			),
			table.Entry(
				"when the claim we're looking for is actually passed in localnet topology",
				&ovncnitypes.NetConf{Topology: ovnktypes.LocalnetTopology, Subnets: "192.10.10.0/24"},
				&nadapi.NetworkSelectionElement{IPAMClaimReference: claimName, Namespace: namespace},
				ipamClaimWithIPs(namespace, claimName, "192.10.10.10/24"),
				ipamClaimWithIPs(namespace, claimName, "192.10.10.10/24"),
			),
		)

		table.DescribeTable(
			"fails",
			func(
				netConf *ovncnitypes.NetConf,
				network *nadapi.NetworkSelectionElement,
				inputClaims *ipamclaimsapi.IPAMClaim,
				expectedError error,
			) {
				ctx, cancel := context.WithCancel(context.Background())
				lister, listerTeardown := generateIPAMClaimsListerAndTeardownFunc(ctx.Done(), inputClaims)
				defer func() {
					cancel()
					listerTeardown()
				}()

				netInfo, err := util.NewNetInfo(netConf)
				Expect(err).NotTo(HaveOccurred())
				_, actualError := NewIPAMClaimReconciler(nil, netInfo, lister).FindIPAMClaim(
					network.IPAMClaimReference,
					network.Namespace,
				)
				Expect(actualError).To(MatchError(expectedError))
			},
			table.Entry(
				"when an empty claim is passed in layer2 topology",
				&ovncnitypes.NetConf{Topology: ovnktypes.Layer2Topology},
				&nadapi.NetworkSelectionElement{IPAMClaimReference: "", Namespace: namespace},
				nil,
				ErrPersistentIPsNotAvailableOnNetwork,
			),
			table.Entry(
				"when an empty claim is passed in localnet topology",
				&ovncnitypes.NetConf{Topology: ovnktypes.LocalnetTopology},
				&nadapi.NetworkSelectionElement{IPAMClaimReference: "", Namespace: namespace},
				nil,
				ErrPersistentIPsNotAvailableOnNetwork,
			),
			table.Entry(
				"when an empty claim is passed in layer3 topology",
				&ovncnitypes.NetConf{Topology: ovnktypes.Layer3Topology},
				&nadapi.NetworkSelectionElement{IPAMClaimReference: "", Namespace: namespace},
				nil,
				ErrPersistentIPsNotAvailableOnNetwork,
			),
			table.Entry(
				"when an empty datastore is passed in layer2 topology",
				&ovncnitypes.NetConf{Topology: ovnktypes.Layer2Topology},
				&nadapi.NetworkSelectionElement{IPAMClaimReference: claimName, Namespace: namespace},
				nil,
				ErrPersistentIPsNotAvailableOnNetwork,
			),
			table.Entry(
				"when an empty datastore is passed in localnet topology",
				&ovncnitypes.NetConf{Topology: ovnktypes.LocalnetTopology},
				&nadapi.NetworkSelectionElement{IPAMClaimReference: claimName, Namespace: namespace},
				nil,
				ErrPersistentIPsNotAvailableOnNetwork,
			),
			table.Entry(
				"when an empty datastore is passed in layer3 topology",
				&ovncnitypes.NetConf{Topology: ovnktypes.Layer3Topology},
				&nadapi.NetworkSelectionElement{IPAMClaimReference: claimName, Namespace: namespace},
				nil,
				ErrPersistentIPsNotAvailableOnNetwork,
			),
			table.Entry(
				"when the claim we're looking for is actually passed in layer2 topology for a network without subnets",
				&ovncnitypes.NetConf{Topology: ovnktypes.Layer2Topology},
				&nadapi.NetworkSelectionElement{IPAMClaimReference: claimName, Namespace: namespace},
				ipamClaimWithIPs(namespace, claimName, networkName, "192.10.10.10/24"),
				ErrPersistentIPsNotAvailableOnNetwork,
			),
			table.Entry(
				"when the claim we're looking for is actually passed in localnet topology for a network without subnets",
				&ovncnitypes.NetConf{Topology: ovnktypes.LocalnetTopology},
				&nadapi.NetworkSelectionElement{IPAMClaimReference: claimName, Namespace: namespace},
				ipamClaimWithIPs(namespace, claimName, networkName, "192.10.10.10/24"),
				ErrPersistentIPsNotAvailableOnNetwork,
			),
			table.Entry(
				"when the claim we're looking for is actually passed in layer3 topology",
				&ovncnitypes.NetConf{Topology: ovnktypes.Layer3Topology, Subnets: "192.10.10.0/16/24"},
				&nadapi.NetworkSelectionElement{IPAMClaimReference: claimName, Namespace: namespace},
				ipamClaimWithIPs(namespace, claimName, "192.10.10.10/24"),
				ErrPersistentIPsNotAvailableOnNetwork,
			),
		)
	})
})

func emptyDummyIPAMClaim(namespace string, claimName string, networkName string) *ipamclaimsapi.IPAMClaim {
	return &ipamclaimsapi.IPAMClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      claimName,
			Namespace: namespace,
		},
		Spec: ipamclaimsapi.IPAMClaimSpec{
			Network: networkName,
		},
	}
}

func ipamClaimWithIPs(namespace string, claimName string, networkName string, ips ...string) *ipamclaimsapi.IPAMClaim {
	return &ipamclaimsapi.IPAMClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      claimName,
			Namespace: namespace,
		},
		Spec: ipamclaimsapi.IPAMClaimSpec{
			Network: networkName,
		},
		Status: ipamclaimsapi.IPAMClaimStatus{
			IPs: ips,
		},
	}
}

func toRuntimeObj(ipamClaims []*ipamclaimsapi.IPAMClaim) []runtime.Object {
	var castIPAMClaims []runtime.Object
	for i := range ipamClaims {
		if ipamClaims[i] == nil {
			continue
		}
		castIPAMClaims = append(castIPAMClaims, ipamClaims[i])
	}
	return castIPAMClaims
}

func generateIPAMClaimsListerAndTeardownFunc(stopChannel <-chan struct{}, ipamClaims ...*ipamclaimsapi.IPAMClaim) (ipamclaimslister.IPAMClaimLister, func()) {
	ipamClaimClient := fakeipamclaimclient.NewSimpleClientset(toRuntimeObj(ipamClaims)...)
	informerFactory := ipamclaimsfactory.NewSharedInformerFactory(ipamClaimClient, 0)
	lister := informerFactory.K8s().V1alpha1().IPAMClaims().Lister()
	informerFactory.Start(stopChannel)
	informerFactory.WaitForCacheSync(stopChannel)
	return lister, func() {
		informerFactory.Shutdown()
	}
}
