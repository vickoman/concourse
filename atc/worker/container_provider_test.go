package worker_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"time"

	"code.cloudfoundry.org/clock/fakeclock"
	"code.cloudfoundry.org/garden"
	"code.cloudfoundry.org/garden/gardenfakes"
	"code.cloudfoundry.org/lager"
	"code.cloudfoundry.org/lager/lagertest"
	"github.com/cloudfoundry/bosh-cli/director/template"
	"github.com/concourse/baggageclaim"
	"github.com/concourse/baggageclaim/baggageclaimfakes"
	"github.com/concourse/concourse/atc"
	"github.com/concourse/concourse/atc/creds"
	"github.com/concourse/concourse/atc/db"
	"github.com/concourse/concourse/atc/db/dbfakes"
	"github.com/concourse/concourse/atc/db/lock/lockfakes"
	. "github.com/concourse/concourse/atc/worker"
	"github.com/concourse/concourse/atc/worker/workerfakes"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("ContainerProvider", func() {
	var (
		logger                    *lagertest.TestLogger
		fakeImageFetchingDelegate *workerfakes.FakeImageFetchingDelegate

		fakeCreatingContainer *dbfakes.FakeCreatingContainer
		fakeCreatedContainer  *dbfakes.FakeCreatedContainer

		fakeGardenClient       *gardenfakes.FakeClient
		fakeGardenContainer    *gardenfakes.FakeContainer
		fakeBaggageclaimClient *baggageclaimfakes.FakeClient
		fakeVolumeClient       *workerfakes.FakeVolumeClient
		fakeImageFactory       *workerfakes.FakeImageFactory
		fakeImage              *workerfakes.FakeImage
		fakeDBTeam             *dbfakes.FakeTeam
		fakeDBWorker           *dbfakes.FakeWorker
		fakeDBVolumeRepository *dbfakes.FakeVolumeRepository
		fakeLockFactory        *lockfakes.FakeLockFactory

		containerProvider ContainerProvider

		fakeLocalInput    *workerfakes.FakeInputSource
		fakeRemoteInput   *workerfakes.FakeInputSource
		fakeRemoteInputAS *workerfakes.FakeArtifactSource

		fakeBindMount *workerfakes.FakeBindMountSource

		fakeRemoteInputContainerVolume *workerfakes.FakeVolume
		fakeLocalVolume                *workerfakes.FakeVolume
		fakeOutputVolume               *workerfakes.FakeVolume
		fakeLocalCOWVolume             *workerfakes.FakeVolume

		ctx                context.Context
		containerSpec      ContainerSpec
		workerSpec         WorkerSpec
		fakeContainerOwner *dbfakes.FakeContainerOwner
		containerMetadata  db.ContainerMetadata
		resourceTypes      creds.VersionedResourceTypes

		findOrCreateErr       error
		findOrCreateContainer Container

		stubbedVolumes map[string]*workerfakes.FakeVolume
		volumeSpecs    map[string]VolumeSpec
	)

	disasterErr := errors.New("disaster")

	BeforeEach(func() {
		logger = lagertest.NewTestLogger("test")

		fakeCreatingContainer = new(dbfakes.FakeCreatingContainer)
		fakeCreatingContainer.HandleReturns("some-handle")
		fakeCreatedContainer = new(dbfakes.FakeCreatedContainer)

		fakeImageFetchingDelegate = new(workerfakes.FakeImageFetchingDelegate)

		fakeGardenClient = new(gardenfakes.FakeClient)
		fakeBaggageclaimClient = new(baggageclaimfakes.FakeClient)
		fakeVolumeClient = new(workerfakes.FakeVolumeClient)
		fakeImageFactory = new(workerfakes.FakeImageFactory)
		fakeImage = new(workerfakes.FakeImage)
		fakeImage.FetchForContainerReturns(FetchedImage{
			Metadata: ImageMetadata{
				Env: []string{"IMAGE=ENV"},
			},
			URL: "some-image-url",
		}, nil)
		fakeImageFactory.GetImageReturns(fakeImage, nil)
		fakeLockFactory = new(lockfakes.FakeLockFactory)

		fakeDBTeamFactory := new(dbfakes.FakeTeamFactory)
		fakeDBTeam = new(dbfakes.FakeTeam)
		fakeDBTeamFactory.GetByIDReturns(fakeDBTeam)
		fakeDBVolumeRepository = new(dbfakes.FakeVolumeRepository)
		fakeClock := fakeclock.NewFakeClock(time.Unix(0, 123))
		fakeGardenContainer = new(gardenfakes.FakeContainer)
		fakeGardenClient.CreateReturns(fakeGardenContainer, nil)

		fakeDBWorker = new(dbfakes.FakeWorker)
		fakeDBWorker.HTTPProxyURLReturns("http://proxy.com")
		fakeDBWorker.HTTPSProxyURLReturns("https://proxy.com")
		fakeDBWorker.NoProxyReturns("http://noproxy.com")

		containerProvider = NewContainerProvider(
			fakeGardenClient,
			fakeBaggageclaimClient,
			fakeVolumeClient,
			fakeDBWorker,
			fakeClock,
			fakeImageFactory,
			fakeDBVolumeRepository,
			fakeDBTeamFactory,
			fakeLockFactory,
		)

		fakeLocalInput = new(workerfakes.FakeInputSource)
		fakeLocalInput.DestinationPathReturns("/some/work-dir/local-input")
		fakeLocalInputAS := new(workerfakes.FakeArtifactSource)
		fakeLocalVolume = new(workerfakes.FakeVolume)
		fakeLocalVolume.PathReturns("/fake/local/volume")
		fakeLocalVolume.COWStrategyReturns(baggageclaim.COWStrategy{
			Parent: new(baggageclaimfakes.FakeVolume),
		})
		fakeLocalInputAS.VolumeOnReturns(fakeLocalVolume, true, nil)
		fakeLocalInput.SourceReturns(fakeLocalInputAS)

		fakeBindMount = new(workerfakes.FakeBindMountSource)
		fakeBindMount.VolumeOnReturns(garden.BindMount{
			SrcPath: "some/source",
			DstPath: "some/destination",
			Mode:    garden.BindMountModeRO,
		}, true, nil)

		fakeRemoteInput = new(workerfakes.FakeInputSource)
		fakeRemoteInput.DestinationPathReturns("/some/work-dir/remote-input")
		fakeRemoteInputAS = new(workerfakes.FakeArtifactSource)
		fakeRemoteInputAS.VolumeOnReturns(nil, false, nil)
		fakeRemoteInput.SourceReturns(fakeRemoteInputAS)

		fakeScratchVolume := new(workerfakes.FakeVolume)
		fakeScratchVolume.PathReturns("/fake/scratch/volume")

		fakeWorkdirVolume := new(workerfakes.FakeVolume)
		fakeWorkdirVolume.PathReturns("/fake/work-dir/volume")

		fakeOutputVolume = new(workerfakes.FakeVolume)
		fakeOutputVolume.PathReturns("/fake/output/volume")

		fakeLocalCOWVolume = new(workerfakes.FakeVolume)
		fakeLocalCOWVolume.PathReturns("/fake/local/cow/volume")

		fakeRemoteInputContainerVolume = new(workerfakes.FakeVolume)
		fakeRemoteInputContainerVolume.PathReturns("/fake/remote/input/container/volume")

		stubbedVolumes = map[string]*workerfakes.FakeVolume{
			"/scratch":                    fakeScratchVolume,
			"/some/work-dir":              fakeWorkdirVolume,
			"/some/work-dir/local-input":  fakeLocalCOWVolume,
			"/some/work-dir/remote-input": fakeRemoteInputContainerVolume,
			"/some/work-dir/output":       fakeOutputVolume,
		}

		volumeSpecs = map[string]VolumeSpec{}

		fakeVolumeClient.FindOrCreateCOWVolumeForContainerStub = func(logger lager.Logger, volumeSpec VolumeSpec, creatingContainer db.CreatingContainer, volume Volume, teamID int, mountPath string) (Volume, error) {
			Expect(volume).To(Equal(fakeLocalVolume))

			volume, found := stubbedVolumes[mountPath]
			if !found {
				panic("unknown container volume: " + mountPath)
			}

			volumeSpecs[mountPath] = volumeSpec

			return volume, nil
		}

		fakeVolumeClient.FindOrCreateVolumeForContainerStub = func(logger lager.Logger, volumeSpec VolumeSpec, creatingContainer db.CreatingContainer, teamID int, mountPath string) (Volume, error) {
			volume, found := stubbedVolumes[mountPath]
			if !found {
				panic("unknown container volume: " + mountPath)
			}

			volumeSpecs[mountPath] = volumeSpec

			return volume, nil
		}

		ctx = context.Background()

		fakeContainerOwner = new(dbfakes.FakeContainerOwner)

		containerMetadata = db.ContainerMetadata{
			StepName: "some-step",
		}

		variables := template.StaticVariables{
			"secret-image":  "super-secret-image",
			"secret-source": "super-secret-source",
		}

		cpu := uint64(1024)
		memory := uint64(1024)
		containerSpec = ContainerSpec{
			TeamID: 73410,

			ImageSpec: ImageSpec{
				ImageResource: &ImageResource{
					Type:   "registry-image",
					Source: creds.NewSource(variables, atc.Source{"some": "((secret-image))"}),
				},
			},

			User: "some-user",
			Env:  []string{"SOME=ENV"},

			Dir: "/some/work-dir",

			Inputs: []InputSource{
				fakeLocalInput,
				fakeRemoteInput,
			},

			Outputs: OutputPaths{
				"some-output": "/some/work-dir/output",
			},
			BindMounts: []BindMountSource{
				fakeBindMount,
			},
			Limits: ContainerLimits{
				CPU:    &cpu,
				Memory: &memory,
			},
		}

		resourceTypes = creds.NewVersionedResourceTypes(variables, atc.VersionedResourceTypes{
			{
				ResourceType: atc.ResourceType{
					Type:   "some-type",
					Source: atc.Source{"some": "((secret-source))"},
				},
				Version: atc.Version{"some": "version"},
			},
		})

		workerSpec = WorkerSpec{
			TeamID:        73410,
			ResourceType:  "registry-image",
			ResourceTypes: resourceTypes,
		}
	})

	CertsVolumeExists := func() {
		fakeCertsVolume := new(baggageclaimfakes.FakeVolume)
		fakeBaggageclaimClient.LookupVolumeReturns(fakeCertsVolume, true, nil)
	}

	Describe("FindOrCreateContainer", func() {
		BeforeEach(func() {
			fakeDBWorker.CreateContainerReturns(fakeCreatingContainer, nil)
			fakeLockFactory.AcquireReturns(new(lockfakes.FakeLock), true, nil)
		})

		JustBeforeEach(func() {
			findOrCreateContainer, findOrCreateErr = containerProvider.FindOrCreateContainer(
				ctx,
				logger,
				fakeContainerOwner,
				fakeImageFetchingDelegate,
				containerMetadata,
				containerSpec,
				workerSpec,
				resourceTypes,
			)
		})

		Context("when container exists in database in creating state", func() {
			BeforeEach(func() {
				fakeDBWorker.FindContainerOnWorkerReturns(fakeCreatingContainer, nil, nil)
			})

			Context("when container exists in garden", func() {
				BeforeEach(func() {
					fakeGardenClient.LookupReturns(fakeGardenContainer, nil)
				})

				It("does not acquire lock", func() {
					Expect(fakeLockFactory.AcquireCallCount()).To(Equal(0))
				})

				It("marks container as created", func() {
					Expect(fakeCreatingContainer.CreatedCallCount()).To(Equal(1))
				})

				It("returns worker container", func() {
					Expect(findOrCreateContainer).ToNot(BeNil())
				})
			})

			Context("when container does not exist in garden", func() {
				BeforeEach(func() {
					fakeGardenClient.LookupReturns(nil, garden.ContainerNotFoundError{})
				})
				BeforeEach(CertsVolumeExists)

				It("gets image", func() {
					Expect(fakeImageFactory.GetImageCallCount()).To(Equal(1))
					Expect(fakeImage.FetchForContainerCallCount()).To(Equal(1))
				})

				It("acquires lock", func() {
					Expect(fakeLockFactory.AcquireCallCount()).To(Equal(1))
				})

				It("creates container in garden", func() {
					Expect(fakeGardenClient.CreateCallCount()).To(Equal(1))
				})

				It("marks container as created", func() {
					Expect(fakeCreatingContainer.CreatedCallCount()).To(Equal(1))
				})

				It("returns worker container", func() {
					Expect(findOrCreateContainer).ToNot(BeNil())
				})

				Context("when failing to create container in garden", func() {
					BeforeEach(func() {
						fakeGardenClient.CreateReturns(nil, disasterErr)
					})

					It("returns an error", func() {
						Expect(findOrCreateErr).To(Equal(disasterErr))
					})

					It("does not mark container as created", func() {
						Expect(fakeCreatingContainer.CreatedCallCount()).To(Equal(0))
					})
				})

				Context("when getting image fails", func() {
					BeforeEach(func() {
						fakeImageFactory.GetImageReturns(nil, disasterErr)
					})

					It("returns an error", func() {
						Expect(findOrCreateErr).To(Equal(disasterErr))
					})

					It("does not create container in garden", func() {
						Expect(fakeGardenClient.CreateCallCount()).To(Equal(0))
					})
				})
			})
		})

		Context("when container exists in database in created state", func() {
			BeforeEach(func() {
				fakeDBWorker.FindContainerOnWorkerReturns(nil, fakeCreatedContainer, nil)
			})

			Context("when container exists in garden", func() {
				BeforeEach(func() {
					fakeGardenClient.LookupReturns(fakeGardenContainer, nil)
				})

				It("returns container", func() {
					Expect(findOrCreateContainer).ToNot(BeNil())
				})
			})

			Context("when container does not exist in garden", func() {
				var containerNotFoundErr error

				BeforeEach(func() {
					containerNotFoundErr = garden.ContainerNotFoundError{}
					fakeGardenClient.LookupReturns(nil, containerNotFoundErr)
				})

				It("returns an error", func() {
					Expect(findOrCreateErr).To(Equal(containerNotFoundErr))
				})
			})
		})

		Context("when container does not exist in database", func() {
			BeforeEach(func() {
				fakeDBWorker.FindContainerOnWorkerReturns(nil, nil, nil)
			})

			Context("when the certs volume does not exist on the worker", func() {
				BeforeEach(func() {
					fakeBaggageclaimClient.LookupVolumeReturns(nil, false, nil)
				})
				It("creates the container in garden, but does not bind mount any certs", func() {
					Expect(fakeGardenClient.CreateCallCount()).To(Equal(1))
					actualSpec := fakeGardenClient.CreateArgsForCall(0)
					Expect(actualSpec.BindMounts).ToNot(ContainElement(
						garden.BindMount{
							SrcPath: "/the/certs/volume/path",
							DstPath: "/etc/ssl/certs",
							Mode:    garden.BindMountModeRO,
						},
					))
				})
			})

			BeforeEach(func() {
				fakeCertsVolume := new(baggageclaimfakes.FakeVolume)
				fakeCertsVolume.PathReturns("/the/certs/volume/path")
				fakeBaggageclaimClient.LookupVolumeReturns(fakeCertsVolume, true, nil)
			})

			It("gets image", func() {
				Expect(fakeImageFactory.GetImageCallCount()).To(Equal(1))
				_, actualWorker, actualVolumeClient, actualImageSpec, actualTeamID, actualDelegate, actualResourceTypes := fakeImageFactory.GetImageArgsForCall(0)

				Expect(actualWorker.GardenClient()).To(Equal(fakeGardenClient))

				Expect(actualVolumeClient).To(Equal(fakeVolumeClient))
				Expect(actualImageSpec).To(Equal(containerSpec.ImageSpec))
				Expect(actualImageSpec).ToNot(BeZero())
				Expect(actualTeamID).To(Equal(containerSpec.TeamID))
				Expect(actualTeamID).ToNot(BeZero())
				Expect(actualDelegate).To(Equal(fakeImageFetchingDelegate))
				Expect(actualResourceTypes).To(Equal(resourceTypes))

				Expect(fakeImage.FetchForContainerCallCount()).To(Equal(1))
				actualCtx, _, actualContainer := fakeImage.FetchForContainerArgsForCall(0)
				Expect(actualContainer).To(Equal(fakeCreatingContainer))
				Expect(actualCtx).To(Equal(ctx))
			})

			It("creates container in database", func() {
				Expect(fakeDBWorker.CreateContainerCallCount()).To(Equal(1))
			})

			It("acquires lock", func() {
				Expect(fakeLockFactory.AcquireCallCount()).To(Equal(1))
			})

			It("creates the container in garden with the input and output volumes in alphabetical order", func() {
				Expect(fakeGardenClient.CreateCallCount()).To(Equal(1))

				actualSpec := fakeGardenClient.CreateArgsForCall(0)
				Expect(actualSpec).To(Equal(garden.ContainerSpec{
					Handle:     "some-handle",
					RootFSPath: "some-image-url",
					Properties: garden.Properties{"user": "some-user"},
					BindMounts: []garden.BindMount{
						{
							SrcPath: "some/source",
							DstPath: "some/destination",
							Mode:    garden.BindMountModeRO,
						},
						{
							SrcPath: "/fake/scratch/volume",
							DstPath: "/scratch",
							Mode:    garden.BindMountModeRW,
						},
						{
							SrcPath: "/fake/work-dir/volume",
							DstPath: "/some/work-dir",
							Mode:    garden.BindMountModeRW,
						},
						{
							SrcPath: "/fake/local/cow/volume",
							DstPath: "/some/work-dir/local-input",
							Mode:    garden.BindMountModeRW,
						},
						{
							SrcPath: "/fake/output/volume",
							DstPath: "/some/work-dir/output",
							Mode:    garden.BindMountModeRW,
						},
						{
							SrcPath: "/fake/remote/input/container/volume",
							DstPath: "/some/work-dir/remote-input",
							Mode:    garden.BindMountModeRW,
						},
					},
					Limits: garden.Limits{
						CPU:    garden.CPULimits{LimitInShares: 1024},
						Memory: garden.MemoryLimits{LimitInBytes: 1024},
					},
					Env: []string{
						"IMAGE=ENV",
						"SOME=ENV",
						"http_proxy=http://proxy.com",
						"https_proxy=https://proxy.com",
						"no_proxy=http://noproxy.com",
					},
				}))
			})

			Context("when the input and output destination paths overlap", func() {
				var (
					fakeRemoteInputUnderInput    *workerfakes.FakeInputSource
					fakeRemoteInputUnderInputAS  *workerfakes.FakeArtifactSource
					fakeRemoteInputUnderOutput   *workerfakes.FakeInputSource
					fakeRemoteInputUnderOutputAS *workerfakes.FakeArtifactSource

					fakeOutputUnderInputVolume                *workerfakes.FakeVolume
					fakeOutputUnderOutputVolume               *workerfakes.FakeVolume
					fakeRemoteInputUnderInputContainerVolume  *workerfakes.FakeVolume
					fakeRemoteInputUnderOutputContainerVolume *workerfakes.FakeVolume
				)

				BeforeEach(func() {
					fakeRemoteInputUnderInput = new(workerfakes.FakeInputSource)
					fakeRemoteInputUnderInput.DestinationPathReturns("/some/work-dir/remote-input/other-input")
					fakeRemoteInputUnderInputAS = new(workerfakes.FakeArtifactSource)
					fakeRemoteInputUnderInputAS.VolumeOnReturns(nil, false, nil)
					fakeRemoteInputUnderInput.SourceReturns(fakeRemoteInputUnderInputAS)

					fakeRemoteInputUnderOutput = new(workerfakes.FakeInputSource)
					fakeRemoteInputUnderOutput.DestinationPathReturns("/some/work-dir/output/input")
					fakeRemoteInputUnderOutputAS = new(workerfakes.FakeArtifactSource)
					fakeRemoteInputUnderOutputAS.VolumeOnReturns(nil, false, nil)
					fakeRemoteInputUnderOutput.SourceReturns(fakeRemoteInputUnderOutputAS)

					fakeOutputUnderInputVolume = new(workerfakes.FakeVolume)
					fakeOutputUnderInputVolume.PathReturns("/fake/output/under/input/volume")
					fakeOutputUnderOutputVolume = new(workerfakes.FakeVolume)
					fakeOutputUnderOutputVolume.PathReturns("/fake/output/other-output/volume")

					fakeRemoteInputUnderInputContainerVolume = new(workerfakes.FakeVolume)
					fakeRemoteInputUnderInputContainerVolume.PathReturns("/fake/remote/input/other-input/container/volume")
					fakeRemoteInputUnderOutputContainerVolume = new(workerfakes.FakeVolume)
					fakeRemoteInputUnderOutputContainerVolume.PathReturns("/fake/output/input/container/volume")

					stubbedVolumes["/some/work-dir/remote-input/other-input"] = fakeRemoteInputUnderInputContainerVolume
					stubbedVolumes["/some/work-dir/output/input"] = fakeRemoteInputUnderOutputContainerVolume
					stubbedVolumes["/some/work-dir/output/other-output"] = fakeOutputUnderOutputVolume
					stubbedVolumes["/some/work-dir/local-input/output"] = fakeOutputUnderInputVolume
				})

				Context("outputs are nested under inputs", func() {
					BeforeEach(func() {
						containerSpec.Inputs = []InputSource{
							fakeLocalInput,
						}
						containerSpec.Outputs = OutputPaths{
							"some-output-under-input": "/some/work-dir/local-input/output",
						}
					})

					It("creates the container with correct bind mounts", func() {
						Expect(fakeGardenClient.CreateCallCount()).To(Equal(1))

						actualSpec := fakeGardenClient.CreateArgsForCall(0)
						Expect(actualSpec).To(Equal(garden.ContainerSpec{
							Handle:     "some-handle",
							RootFSPath: "some-image-url",
							Properties: garden.Properties{"user": "some-user"},
							BindMounts: []garden.BindMount{
								{
									SrcPath: "some/source",
									DstPath: "some/destination",
									Mode:    garden.BindMountModeRO,
								},
								{
									SrcPath: "/fake/scratch/volume",
									DstPath: "/scratch",
									Mode:    garden.BindMountModeRW,
								},
								{
									SrcPath: "/fake/work-dir/volume",
									DstPath: "/some/work-dir",
									Mode:    garden.BindMountModeRW,
								},
								{
									SrcPath: "/fake/local/cow/volume",
									DstPath: "/some/work-dir/local-input",
									Mode:    garden.BindMountModeRW,
								},
								{
									SrcPath: "/fake/output/under/input/volume",
									DstPath: "/some/work-dir/local-input/output",
									Mode:    garden.BindMountModeRW,
								},
							},
							Limits: garden.Limits{
								CPU:    garden.CPULimits{LimitInShares: 1024},
								Memory: garden.MemoryLimits{LimitInBytes: 1024},
							},
							Env: []string{
								"IMAGE=ENV",
								"SOME=ENV",
								"http_proxy=http://proxy.com",
								"https_proxy=https://proxy.com",
								"no_proxy=http://noproxy.com",
							},
						}))
					})
				})

				Context("inputs are nested under inputs", func() {
					BeforeEach(func() {
						containerSpec.Inputs = []InputSource{
							fakeRemoteInput,
							fakeRemoteInputUnderInput,
						}
						containerSpec.Outputs = OutputPaths{}
					})

					It("creates the container with correct bind mounts", func() {
						Expect(fakeGardenClient.CreateCallCount()).To(Equal(1))

						actualSpec := fakeGardenClient.CreateArgsForCall(0)
						Expect(actualSpec).To(Equal(garden.ContainerSpec{
							Handle:     "some-handle",
							RootFSPath: "some-image-url",
							Properties: garden.Properties{"user": "some-user"},
							BindMounts: []garden.BindMount{
								{
									SrcPath: "some/source",
									DstPath: "some/destination",
									Mode:    garden.BindMountModeRO,
								},
								{
									SrcPath: "/fake/scratch/volume",
									DstPath: "/scratch",
									Mode:    garden.BindMountModeRW,
								},
								{
									SrcPath: "/fake/work-dir/volume",
									DstPath: "/some/work-dir",
									Mode:    garden.BindMountModeRW,
								},
								{
									SrcPath: "/fake/remote/input/container/volume",
									DstPath: "/some/work-dir/remote-input",
									Mode:    garden.BindMountModeRW,
								},
								{
									SrcPath: "/fake/remote/input/other-input/container/volume",
									DstPath: "/some/work-dir/remote-input/other-input",
									Mode:    garden.BindMountModeRW,
								},
							},
							Limits: garden.Limits{
								CPU:    garden.CPULimits{LimitInShares: 1024},
								Memory: garden.MemoryLimits{LimitInBytes: 1024},
							},
							Env: []string{
								"IMAGE=ENV",
								"SOME=ENV",
								"http_proxy=http://proxy.com",
								"https_proxy=https://proxy.com",
								"no_proxy=http://noproxy.com",
							},
						}))
					})
				})

				Context("outputs are nested under outputs", func() {
					BeforeEach(func() {
						containerSpec.Inputs = []InputSource{}
						containerSpec.Outputs = OutputPaths{
							"some-output":              "/some/work-dir/output",
							"some-output-under-output": "/some/work-dir/output/other-output",
						}
					})

					It("creates the container with correct bind mounts", func() {
						Expect(fakeGardenClient.CreateCallCount()).To(Equal(1))

						actualSpec := fakeGardenClient.CreateArgsForCall(0)
						Expect(actualSpec).To(Equal(garden.ContainerSpec{
							Handle:     "some-handle",
							RootFSPath: "some-image-url",
							Properties: garden.Properties{"user": "some-user"},
							BindMounts: []garden.BindMount{
								{
									SrcPath: "some/source",
									DstPath: "some/destination",
									Mode:    garden.BindMountModeRO,
								},
								{
									SrcPath: "/fake/scratch/volume",
									DstPath: "/scratch",
									Mode:    garden.BindMountModeRW,
								},
								{
									SrcPath: "/fake/work-dir/volume",
									DstPath: "/some/work-dir",
									Mode:    garden.BindMountModeRW,
								},
								{
									SrcPath: "/fake/output/volume",
									DstPath: "/some/work-dir/output",
									Mode:    garden.BindMountModeRW,
								},
								{
									SrcPath: "/fake/output/other-output/volume",
									DstPath: "/some/work-dir/output/other-output",
									Mode:    garden.BindMountModeRW,
								},
							},
							Limits: garden.Limits{
								CPU:    garden.CPULimits{LimitInShares: 1024},
								Memory: garden.MemoryLimits{LimitInBytes: 1024},
							},
							Env: []string{
								"IMAGE=ENV",
								"SOME=ENV",
								"http_proxy=http://proxy.com",
								"https_proxy=https://proxy.com",
								"no_proxy=http://noproxy.com",
							},
						}))
					})
				})

				Context("inputs are nested under outputs", func() {
					BeforeEach(func() {
						containerSpec.Inputs = []InputSource{
							fakeRemoteInputUnderOutput,
						}
						containerSpec.Outputs = OutputPaths{
							"some-output": "/some/work-dir/output",
						}
					})

					It("creates the container with correct bind mounts", func() {
						Expect(fakeGardenClient.CreateCallCount()).To(Equal(1))

						actualSpec := fakeGardenClient.CreateArgsForCall(0)
						Expect(actualSpec).To(Equal(garden.ContainerSpec{
							Handle:     "some-handle",
							RootFSPath: "some-image-url",
							Properties: garden.Properties{"user": "some-user"},
							BindMounts: []garden.BindMount{
								{
									SrcPath: "some/source",
									DstPath: "some/destination",
									Mode:    garden.BindMountModeRO,
								},
								{
									SrcPath: "/fake/scratch/volume",
									DstPath: "/scratch",
									Mode:    garden.BindMountModeRW,
								},
								{
									SrcPath: "/fake/work-dir/volume",
									DstPath: "/some/work-dir",
									Mode:    garden.BindMountModeRW,
								},
								{
									SrcPath: "/fake/output/volume",
									DstPath: "/some/work-dir/output",
									Mode:    garden.BindMountModeRW,
								},
								{
									SrcPath: "/fake/output/input/container/volume",
									DstPath: "/some/work-dir/output/input",
									Mode:    garden.BindMountModeRW,
								},
							},
							Limits: garden.Limits{
								CPU:    garden.CPULimits{LimitInShares: 1024},
								Memory: garden.MemoryLimits{LimitInBytes: 1024},
							},
							Env: []string{
								"IMAGE=ENV",
								"SOME=ENV",
								"http_proxy=http://proxy.com",
								"https_proxy=https://proxy.com",
								"no_proxy=http://noproxy.com",
							},
						}))

					})
				})

				Context("input and output share the same destination path", func() {
					BeforeEach(func() {
						containerSpec.Inputs = []InputSource{
							fakeRemoteInput,
						}
						containerSpec.Outputs = OutputPaths{
							"some-output": "/some/work-dir/remote-input",
						}
					})

					It("creates the container with correct bind mounts", func() {
						Expect(fakeGardenClient.CreateCallCount()).To(Equal(1))

						actualSpec := fakeGardenClient.CreateArgsForCall(0)
						Expect(actualSpec).To(Equal(garden.ContainerSpec{
							Handle:     "some-handle",
							RootFSPath: "some-image-url",
							Properties: garden.Properties{"user": "some-user"},
							BindMounts: []garden.BindMount{
								{
									SrcPath: "some/source",
									DstPath: "some/destination",
									Mode:    garden.BindMountModeRO,
								},
								{
									SrcPath: "/fake/scratch/volume",
									DstPath: "/scratch",
									Mode:    garden.BindMountModeRW,
								},
								{
									SrcPath: "/fake/work-dir/volume",
									DstPath: "/some/work-dir",
									Mode:    garden.BindMountModeRW,
								},
								{
									SrcPath: "/fake/remote/input/container/volume",
									DstPath: "/some/work-dir/remote-input",
									Mode:    garden.BindMountModeRW,
								},
							},
							Limits: garden.Limits{
								CPU:    garden.CPULimits{LimitInShares: 1024},
								Memory: garden.MemoryLimits{LimitInBytes: 1024},
							},
							Env: []string{
								"IMAGE=ENV",
								"SOME=ENV",
								"http_proxy=http://proxy.com",
								"https_proxy=https://proxy.com",
								"no_proxy=http://noproxy.com",
							},
						}))
					})

				})
			})

			It("creates each volume unprivileged", func() {
				Expect(volumeSpecs).To(Equal(map[string]VolumeSpec{
					"/scratch":                    VolumeSpec{Strategy: baggageclaim.EmptyStrategy{}},
					"/some/work-dir":              VolumeSpec{Strategy: baggageclaim.EmptyStrategy{}},
					"/some/work-dir/output":       VolumeSpec{Strategy: baggageclaim.EmptyStrategy{}},
					"/some/work-dir/local-input":  VolumeSpec{Strategy: fakeLocalVolume.COWStrategy()},
					"/some/work-dir/remote-input": VolumeSpec{Strategy: baggageclaim.EmptyStrategy{}},
				}))
			})

			It("streams remote inputs into newly created container volumes", func() {
				Expect(fakeRemoteInputAS.StreamToCallCount()).To(Equal(1))
				_, ad := fakeRemoteInputAS.StreamToArgsForCall(0)

				err := ad.StreamIn(".", bytes.NewBufferString("some-stream"))
				Expect(err).ToNot(HaveOccurred())

				Expect(fakeRemoteInputContainerVolume.StreamInCallCount()).To(Equal(1))

				dst, from := fakeRemoteInputContainerVolume.StreamInArgsForCall(0)
				Expect(dst).To(Equal("."))
				Expect(ioutil.ReadAll(from)).To(Equal([]byte("some-stream")))
			})

			It("marks container as created", func() {
				Expect(fakeCreatingContainer.CreatedCallCount()).To(Equal(1))
			})

			Context("when the fetched image was privileged", func() {
				BeforeEach(func() {
					fakeImage.FetchForContainerReturns(FetchedImage{
						Privileged: true,
						Metadata: ImageMetadata{
							Env: []string{"IMAGE=ENV"},
						},
						URL: "some-image-url",
					}, nil)
				})

				It("creates the container privileged", func() {
					Expect(fakeGardenClient.CreateCallCount()).To(Equal(1))

					actualSpec := fakeGardenClient.CreateArgsForCall(0)
					Expect(actualSpec.Privileged).To(BeTrue())
				})

				It("creates each volume privileged", func() {
					Expect(volumeSpecs).To(Equal(map[string]VolumeSpec{
						"/scratch":                    VolumeSpec{Privileged: true, Strategy: baggageclaim.EmptyStrategy{}},
						"/some/work-dir":              VolumeSpec{Privileged: true, Strategy: baggageclaim.EmptyStrategy{}},
						"/some/work-dir/output":       VolumeSpec{Privileged: true, Strategy: baggageclaim.EmptyStrategy{}},
						"/some/work-dir/local-input":  VolumeSpec{Privileged: true, Strategy: fakeLocalVolume.COWStrategy()},
						"/some/work-dir/remote-input": VolumeSpec{Privileged: true, Strategy: baggageclaim.EmptyStrategy{}},
					}))
				})

			})

			Context("when an input has the path set to the workdir itself", func() {
				BeforeEach(func() {
					fakeLocalInput.DestinationPathReturns("/some/work-dir")
					delete(stubbedVolumes, "/some/work-dir/local-input")
					stubbedVolumes["/some/work-dir"] = fakeLocalCOWVolume
				})

				It("does not create or mount a work-dir, as we support this for backwards-compatibility", func() {
					Expect(fakeGardenClient.CreateCallCount()).To(Equal(1))

					actualSpec := fakeGardenClient.CreateArgsForCall(0)
					Expect(actualSpec.BindMounts).To(Equal([]garden.BindMount{
						{
							SrcPath: "some/source",
							DstPath: "some/destination",
							Mode:    garden.BindMountModeRO,
						},
						{
							SrcPath: "/fake/scratch/volume",
							DstPath: "/scratch",
							Mode:    garden.BindMountModeRW,
						},
						{
							SrcPath: "/fake/local/cow/volume",
							DstPath: "/some/work-dir",
							Mode:    garden.BindMountModeRW,
						},
						{
							SrcPath: "/fake/output/volume",
							DstPath: "/some/work-dir/output",
							Mode:    garden.BindMountModeRW,
						},
						{
							SrcPath: "/fake/remote/input/container/volume",
							DstPath: "/some/work-dir/remote-input",
							Mode:    garden.BindMountModeRW,
						},
					}))
				})
			})

			Context("when getting image fails", func() {
				BeforeEach(func() {
					fakeImageFactory.GetImageReturns(nil, disasterErr)
				})

				It("returns an error", func() {
					Expect(findOrCreateErr).To(Equal(disasterErr))
				})

				It("does not create container in database", func() {
					Expect(fakeDBWorker.CreateContainerCallCount()).To(Equal(0))
				})

				It("does not create container in garden", func() {
					Expect(fakeGardenClient.CreateCallCount()).To(Equal(0))
				})
			})

			Context("when failing to create container in garden", func() {
				BeforeEach(func() {
					fakeGardenClient.CreateReturns(nil, disasterErr)
				})

				It("returns an error", func() {
					Expect(findOrCreateErr).To(Equal(disasterErr))
				})

				It("does not mark container as created", func() {
					Expect(fakeCreatingContainer.CreatedCallCount()).To(Equal(0))
				})

				It("marks the container as failed", func() {
					Expect(fakeCreatingContainer.FailedCallCount()).To(Equal(1))
				})
			})
		})
	})

	Describe("FindCreatedContainerByHandle", func() {
		var (
			foundContainer Container
			findErr        error
			found          bool
		)

		JustBeforeEach(func() {
			foundContainer, found, findErr = containerProvider.FindCreatedContainerByHandle(logger, "some-container-handle", 42)
		})

		Context("when the gardenClient returns a container and no error", func() {
			var (
				fakeContainer *gardenfakes.FakeContainer
			)

			BeforeEach(func() {
				fakeContainer = new(gardenfakes.FakeContainer)
				fakeContainer.HandleReturns("provider-handle")

				fakeDBVolumeRepository.FindVolumesForContainerReturns([]db.CreatedVolume{}, nil)

				fakeDBTeam.FindCreatedContainerByHandleReturns(fakeCreatedContainer, true, nil)
				fakeGardenClient.LookupReturns(fakeContainer, nil)
			})

			It("returns the container", func() {
				Expect(findErr).NotTo(HaveOccurred())
				Expect(found).To(BeTrue())
				Expect(foundContainer.Handle()).To(Equal(fakeContainer.Handle()))
			})

			Describe("the found container", func() {
				It("can be destroyed", func() {
					err := foundContainer.Destroy()
					Expect(err).NotTo(HaveOccurred())

					By("destroying via garden")
					Expect(fakeGardenClient.DestroyCallCount()).To(Equal(1))
					Expect(fakeGardenClient.DestroyArgsForCall(0)).To(Equal("provider-handle"))
				})
			})

			Context("when the concourse:volumes property is present", func() {
				var (
					expectedHandle1Volume *workerfakes.FakeVolume
					expectedHandle2Volume *workerfakes.FakeVolume
				)

				BeforeEach(func() {
					expectedHandle1Volume = new(workerfakes.FakeVolume)
					expectedHandle2Volume = new(workerfakes.FakeVolume)

					expectedHandle1Volume.HandleReturns("handle-1")
					expectedHandle2Volume.HandleReturns("handle-2")

					expectedHandle1Volume.PathReturns("/handle-1/path")
					expectedHandle2Volume.PathReturns("/handle-2/path")

					fakeVolumeClient.LookupVolumeStub = func(logger lager.Logger, handle string) (Volume, bool, error) {
						if handle == "handle-1" {
							return expectedHandle1Volume, true, nil
						} else if handle == "handle-2" {
							return expectedHandle2Volume, true, nil
						} else {
							panic("unknown handle: " + handle)
						}
					}

					dbVolume1 := new(dbfakes.FakeCreatedVolume)
					dbVolume2 := new(dbfakes.FakeCreatedVolume)
					fakeDBVolumeRepository.FindVolumesForContainerReturns([]db.CreatedVolume{dbVolume1, dbVolume2}, nil)
					dbVolume1.HandleReturns("handle-1")
					dbVolume2.HandleReturns("handle-2")
					dbVolume1.PathReturns("/handle-1/path")
					dbVolume2.PathReturns("/handle-2/path")
				})

				Describe("VolumeMounts", func() {
					It("returns all bound volumes based on properties on the container", func() {
						Expect(findErr).NotTo(HaveOccurred())
						Expect(found).To(BeTrue())
						Expect(foundContainer.VolumeMounts()).To(ConsistOf([]VolumeMount{
							{Volume: expectedHandle1Volume, MountPath: "/handle-1/path"},
							{Volume: expectedHandle2Volume, MountPath: "/handle-2/path"},
						}))
					})

					Context("when LookupVolume returns an error", func() {
						disaster := errors.New("nope")

						BeforeEach(func() {
							fakeVolumeClient.LookupVolumeReturns(nil, false, disaster)
						})

						It("returns the error on lookup", func() {
							Expect(findErr).To(Equal(disaster))
						})
					})
				})
			})

			Context("when the user property is present", func() {
				var (
					actualSpec garden.ProcessSpec
					actualIO   garden.ProcessIO
				)

				BeforeEach(func() {
					actualSpec = garden.ProcessSpec{
						Path: "some-path",
						Args: []string{"some", "args"},
						Env:  []string{"some=env"},
						Dir:  "some-dir",
					}

					actualIO = garden.ProcessIO{}

					fakeContainer.PropertiesReturns(garden.Properties{"user": "maverick"}, nil)
				})

				JustBeforeEach(func() {
					foundContainer.Run(actualSpec, actualIO)
				})

				Describe("Run", func() {
					It("calls Run() on the garden container and injects the user", func() {
						Expect(fakeContainer.RunCallCount()).To(Equal(1))
						spec, io := fakeContainer.RunArgsForCall(0)
						Expect(spec).To(Equal(garden.ProcessSpec{
							Path: "some-path",
							Args: []string{"some", "args"},
							Env:  []string{"some=env"},
							Dir:  "some-dir",
							User: "maverick",
						}))
						Expect(io).To(Equal(garden.ProcessIO{}))
					})
				})
			})

			Context("when the user property is not present", func() {
				var (
					actualSpec garden.ProcessSpec
					actualIO   garden.ProcessIO
				)

				BeforeEach(func() {
					actualSpec = garden.ProcessSpec{
						Path: "some-path",
						Args: []string{"some", "args"},
						Env:  []string{"some=env"},
						Dir:  "some-dir",
					}

					actualIO = garden.ProcessIO{}

					fakeContainer.PropertiesReturns(garden.Properties{"user": ""}, nil)
				})

				JustBeforeEach(func() {
					foundContainer.Run(actualSpec, actualIO)
				})

				Describe("Run", func() {
					It("calls Run() on the garden container and injects the default user", func() {
						Expect(fakeContainer.RunCallCount()).To(Equal(1))
						spec, io := fakeContainer.RunArgsForCall(0)
						Expect(spec).To(Equal(garden.ProcessSpec{
							Path: "some-path",
							Args: []string{"some", "args"},
							Env:  []string{"some=env"},
							Dir:  "some-dir",
							User: "root",
						}))
						Expect(io).To(Equal(garden.ProcessIO{}))
						Expect(fakeContainer.RunCallCount()).To(Equal(1))
					})
				})
			})
		})

		Context("when the gardenClient returns garden.ContainerNotFoundError", func() {
			BeforeEach(func() {
				fakeGardenClient.LookupReturns(nil, garden.ContainerNotFoundError{Handle: "some-handle"})
			})

			It("returns false and no error", func() {
				Expect(findErr).ToNot(HaveOccurred())
				Expect(found).To(BeFalse())
			})
		})

		Context("when the gardenClient returns an error", func() {
			var expectedErr error

			BeforeEach(func() {
				expectedErr = fmt.Errorf("container not found")
				fakeGardenClient.LookupReturns(nil, expectedErr)
			})

			It("returns nil and forwards the error", func() {
				Expect(findErr).To(Equal(expectedErr))

				Expect(foundContainer).To(BeNil())
			})
		})
	})
})
