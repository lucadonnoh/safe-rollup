package p2p

import (
	"github.com/ethereum-optimism/optimism/op-service/apis"
)

var NamespaceRPC = "opp2p"

type API = apis.P2PClient
type PeerInfo = apis.PeerInfo
type PeerDump = apis.PeerDump
type PeerStats = apis.PeerStats
