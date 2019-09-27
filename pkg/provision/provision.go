package provision

import (
	"fmt"
	"github.com/moshloop/platform-cli/pkg/types"
	"sync"
	"time"

	"gopkg.in/yaml.v2"

	konfigadm "github.com/moshloop/konfigadm/pkg/types"
	"github.com/moshloop/platform-cli/pkg/phases"
	"github.com/moshloop/platform-cli/pkg/platform"
	"github.com/moshloop/platform-cli/pkg/provision/vmware"
	"github.com/moshloop/platform-cli/pkg/utils"
	log "github.com/sirupsen/logrus"
)

// VM provisions a new standalone VM
func VM(platform *platform.Platform, vm *types.VM, konfigs ...string) error {

	vmware.LoadGovcEnvVars(vm)
	konfig, err := konfigadm.NewConfig(konfigs...).Build()
	if err != nil {
		return err
	}
	ip, err := platform.Clone(*vm, konfig)

	if err != nil {
		return err
	}
	log.Infof("Provisioned  %s ->  %s\n", vm.Name, ip)
	return nil
}

// Cluster provision or create a kubernetes cluster
func Cluster(platform *platform.Platform) error {

	if err := platform.OpenViaEnv(); err != nil {
		log.Fatalf("Failed to initialize platform: %s", err)
	}

	masters := platform.GetMasterIPs()
	vmware.LoadGovcEnvVars(&platform.Master)
	if len(masters) == 0 {
		vm := platform.Master
		vm.Name = fmt.Sprintf("%s-%s-%s-%s", platform.HostPrefix, platform.Name, "m", utils.ShortTimestamp())

		log.Infof("No  masters detected, deploying new master %s", vm.Name)
		config, err := phases.CreatePrimaryMaster(platform)
		if err != nil {
			log.Fatalf("Failed to create primary master: %s", err)
		}

		data, err := yaml.Marshal(platform)
		if err != nil {
			log.Fatalf("Erroring saving config %s", err)
		}

		log.Tracef("Using configuration: \n%s\n", string(data))

		if !platform.DryRun {
			ip, err := platform.Clone(vm, config)

			if err != nil {
				return err
			}
			log.Infof("Provisioned new master: %s\n", ip)
		}
		if err := platform.WaitFor(); err != nil {
			log.Fatalf("Primary master failed to come up %s ", err)
		}
	}

	masters = platform.GetMasterIPs()
	log.Infof("Detected %d existing masters: %s", len(masters), masters)
	wg := sync.WaitGroup{}
	for i := 0; i < platform.Master.Count-len(masters); i++ {
		time.Sleep(1 * time.Second)
		wg.Add(1)
		go func() {
			vm := platform.Master
			vm.Name = fmt.Sprintf("%s-%s-%s-%s", platform.HostPrefix, platform.Name, "m", utils.ShortTimestamp())
			log.Infof("Creating new secondary master %s\n", vm.Name)
			config, err := phases.CreateSecondaryMaster(platform)
			if err != nil {
				log.Errorf("Failed to create secondary master: %s", err)
			} else {
				if !platform.DryRun {
					ip, err := platform.Clone(vm, config)
					if err != nil {
						log.Errorf("Failed to Clone secondary master: %s", err)
					} else {
						log.Infof("Provisioned new master: %s\n", ip)
					}
				}
			}
			wg.Done()
		}()
	}

	for _, worker := range platform.Nodes {
		vmware.LoadGovcEnvVars(&worker)
		for i := 0; i < worker.Count; i++ {
			time.Sleep(1 * time.Second)
			wg.Add(1)
			vm := worker
			go func() {
				config, err := phases.CreateWorker(platform)
				if err != nil {
					log.Errorf("Failed to create workers %s\n", err)
				} else {
					vm.Name = fmt.Sprintf("%s-%s-%s-%s", platform.HostPrefix, platform.Name, "w", utils.ShortTimestamp())
					if !platform.DryRun {
						log.Infof("Creating new worker %s\n", vm.Name)
						ip, err := platform.Clone(vm, config)
						if err != nil {
							log.Errorf("Failed to Clone worker: %s", err)
						} else {
							log.Infof("Provisioned new worker: %s\n", ip)
						}
					}
				}
				wg.Done()
			}()

		}
	}
	wg.Wait()

	path, err := platform.GetKubeConfig()
	if err != nil {
		return err
	}
	fmt.Printf("\n\n\n A new cluster called %s has been provisioned, access it via: kubectl --kubeconfig %s get nodes\n\n Next deploy the CNI and addons\n\n\n", platform.Name, path)
	return nil
}