package mic

import (
	"encoding/json"
	"os"
	"time"

	cloudprovider "github.com/Azure/aad-pod-identity/pkg/cloudprovider"
	aadpodidconfig "github.com/Azure/aad-pod-identity/pkg/config"
	crd "github.com/Azure/aad-pod-identity/pkg/crd"
	"github.com/golang/glog"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Client has the required pointers to talk to the api server
// and interact with the CRD related datastructure.
type Client struct {
	CRDClient    *crd.CrdClient
	ClientSet    *kubernetes.Clientset
	K8sInformers informers.SharedInformerFactory
	CredConfig   *aadpodidconfig.Config
	CloudClient  *cloudprovider.Client
}

func Cleanup() {

}

func NewMICClient(config *rest.Config, credConfigFile string) (*Client, error) {
	glog.Infof("Starting to create the pod identity client")

	crdClient, err := crd.NewCRDClient(config)
	if err != nil {
		return nil, err
	}

	clientSet := kubernetes.NewForConfigOrDie(config)
	k8sInformers := informers.NewSharedInformerFactory(clientSet, time.Minute*5)

	glog.Infof("Going to open the file: %s", credConfigFile)
	var conf aadpodidconfig.Config
	f, err := os.Open(credConfigFile)
	if err != nil {
		Cleanup()
		glog.Error(err)
		return nil, err
	}

	glog.Infof("Going to decode: %+v\n", f)
	jsonStream := json.NewDecoder(f)
	err = jsonStream.Decode(&conf)
	if err != nil {
		glog.Error(err)
		return nil, err
	}
	glog.Infof("%+v\n", conf)

	cloudClient, err := cloudprovider.NewCloudProvider(conf)
	if err != nil {
		return nil, err
	}

	return &Client{
		CRDClient:    crdClient,
		ClientSet:    clientSet,
		K8sInformers: k8sInformers,
		CredConfig:   &conf,
		CloudClient:  cloudClient,
	}, nil
}

func (c *Client) RemoveAssignedIdentities(podName string, podNameSpace string) (err error) {
	assignedIds, err := c.CRDClient.ListAssignedIds()
	if err != nil {
		return err
	}
	for _, v := range assignedIds.Items {
		if v.Spec.Pod == podName && v.Spec.PodNamespace == podNameSpace {
			glog.Info("Removing the assigned Id with name: %s", v.Name)
			err := c.CRDClient.RemoveAssignedIdentity(v.Name)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// MatchBinding - matches the name of the pod with the bindings. Return back
// the name of the identity which is matching. This name
// will be used to assign the azureidentity to the pod.
func (c *Client) AssignIdentities(podName string, podNameSpace string, nodeName string) (err error) {
	// List the AzureIdentityBindings and check if the pod name matches
	// any selector.
	glog.Infof("Created pod with Name: %s", podName)
	bindings, err := c.CRDClient.ListBindings()
	if err != nil {
		glog.Error(err)
		return err
	}
	for _, v := range bindings.Items {
		glog.Infof("Matching pod name %s with binding name %s", podName, v.Spec.MatchName)
		if v.Spec.MatchName == podName {
			glog.Infof("%+v", v.Spec)
			idName := v.Spec.AzureIdentityRef
			err = c.CRDClient.CreateAssignIdentity(idName, podName, podNameSpace, nodeName)
			if err != nil {
				return err
			}
			glog.Infof("Looking up id: %s", idName)
			id, err := c.CRDClient.Lookup(idName)
			if err != nil {
				glog.Error(err)
				return err
			}
			glog.Infof("Assigning MSI ID: %s to node %s", id.Spec.ResourceID, nodeName)
			err = c.CloudClient.AssignUserMSI(id.Spec.ResourceID, nodeName, c.CredConfig)
			if err != nil {
				glog.Error(err)
				return err
			}
		}
	}
	return nil
}