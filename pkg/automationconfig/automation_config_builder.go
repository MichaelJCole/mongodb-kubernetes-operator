package automationconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
)

type Topology string

const (
	ReplicaSetTopology Topology = "ReplicaSet"
)

type Enabler interface {
	Enable(auth Auth) (Auth, error)
}

type Builder struct {
	enabler           Enabler
	processes         []Process
	replicaSets       []ReplicaSet
	version           int
	auth              Auth
	members           int
	domain            string
	name              string
	fcv               string
	topology          Topology
	mongodbVersion    string
	previousAC        AutomationConfig
	tlsCAFile         string
	tlsCertAndKeyFile string
	tlsMode           SSLMode
	// MongoDB installable versions
	versions []MongoDbVersionConfig
}

func NewBuilder() *Builder {
	return &Builder{
		processes:   []Process{},
		replicaSets: []ReplicaSet{},
		versions:    []MongoDbVersionConfig{},
	}
}

func (b *Builder) SetEnabler(enabler Enabler) *Builder {
	b.enabler = enabler
	return b
}

func (b *Builder) SetTopology(topology Topology) *Builder {
	b.topology = topology
	return b
}

func (b *Builder) SetMembers(members int) *Builder {
	b.members = members
	return b
}

func (b *Builder) SetDomain(domain string) *Builder {
	b.domain = domain
	return b
}

func (b *Builder) SetName(name string) *Builder {
	b.name = name
	return b
}

func (b *Builder) SetFCV(fcv string) *Builder {
	b.fcv = fcv
	return b
}

func (b *Builder) SetTLS(caFile, certAndKeyFile string, mode SSLMode) *Builder {
	b.tlsCAFile = caFile
	b.tlsCertAndKeyFile = certAndKeyFile
	b.tlsMode = mode
	return b
}

func (b *Builder) isTLSEnabled() bool {
	return b.tlsCAFile != "" && b.tlsCertAndKeyFile != "" && b.tlsMode != SSLModeDisabled
}

func (b *Builder) AddVersion(version MongoDbVersionConfig) *Builder {
	for idx := range version.Builds {
		if version.Builds[idx].Modules == nil {
			version.Builds[idx].Modules = make([]string, 0)
		}
	}
	b.versions = append(b.versions, version)
	return b
}

func (b *Builder) SetMongoDBVersion(version string) *Builder {
	b.mongodbVersion = version
	return b
}

func (b *Builder) SetPreviousAutomationConfig(previousAC AutomationConfig) *Builder {
	b.previousAC = previousAC
	return b
}
func (b *Builder) Build() (AutomationConfig, error) {
	hostnames := make([]string, b.members)
	for i := 0; i < b.members; i++ {
		hostnames[i] = fmt.Sprintf("%s-%d.%s", b.name, i, b.domain)
	}

	members := make([]ReplicaSetMember, b.members)
	processes := make([]Process, b.members)
	for i, h := range hostnames {
		opts := []func(*Process){
			withFCV(b.fcv),
		}

		// Configure TLS for mongod if enabled
		if b.isTLSEnabled() {
			opts = append(opts, withTLS(b.tlsCAFile, b.tlsCertAndKeyFile, b.tlsMode))
		}

		process := newProcess(toHostName(b.name, i), h, b.mongodbVersion, b.name, opts...)
		processes[i] = process
		members[i] = newReplicaSetMember(process, i)
	}

	auth, err := b.enabler.Enable(DisabledAuth())
	if err != nil {
		return AutomationConfig{}, err
	}

	currentAc := AutomationConfig{
		Version:   b.previousAC.Version,
		Processes: processes,
		ReplicaSets: []ReplicaSet{
			{
				Id:              b.name,
				Members:         members,
				ProtocolVersion: "1",
			},
		},
		Versions: b.versions,
		Options:  Options{DownloadBase: "/var/lib/mongodb-mms-automation"},
		Auth:     auth,
		SSL: SSL{
			ClientCertificateMode: ClientCertificateModeOptional,
		},
	}

	// Set up TLS between agent and server
	// Agent needs to trust the certificate presented by the server
	if b.isTLSEnabled() {
		currentAc.SSL.CAFilePath = b.tlsCAFile
	}

	// Here we compare the bytes of the two automationconfigs,
	// we can't use reflect.DeepEqual() as it treats nil entries as different from empty ones,
	// and in the AutomationConfig Struct we use omitempty to set empty field to nil
	// The agent requires the nil value we provide, otherwise the agent attempts to configure authentication.

	newAcBytes, err := json.Marshal(b.previousAC)
	if err != nil {
		return AutomationConfig{}, err
	}

	currentAcBytes, err := json.Marshal(currentAc)
	if err != nil {
		return AutomationConfig{}, err
	}

	if bytes.Compare(newAcBytes, currentAcBytes) != 0 {
		currentAc.Version += 1
	}
	return currentAc, nil
}

func toHostName(name string, index int) string {
	return fmt.Sprintf("%s-%d", name, index)
}

// Process functional options
func withFCV(fcv string) func(*Process) {
	return func(process *Process) {
		process.FeatureCompatibilityVersion = fcv
	}
}

// withTLS enables TLS for the mongod process
func withTLS(caFile, tlsKeyFile string, mode SSLMode) func(*Process) {
	return func(process *Process) {
		process.Args26.Net.SSL = MongoDBSSL{
			Mode:                               mode,
			CAFile:                             caFile,
			PEMKeyFile:                         tlsKeyFile,
			AllowConnectionsWithoutCertificate: true,
		}
	}
}
