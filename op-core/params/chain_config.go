// Package params holds the OP-Stack-specific chain configuration types. Consumers
// import it as opparams.
package params

// OptimismConfig holds the OP-Stack-specific EIP-1559 parameters of a chain.
//
// The JSON field names mirror op-geth's params.OptimismConfig verbatim: the value
// is carried as rollup.Config.ChainOpConfig, which is serialised on the wire, so the
// encoding must stay byte-for-byte identical.
type OptimismConfig struct {
	EIP1559Elasticity        uint64  `json:"eip1559Elasticity"`
	EIP1559Denominator       uint64  `json:"eip1559Denominator"`
	EIP1559DenominatorCanyon *uint64 `json:"eip1559DenominatorCanyon,omitempty"`
}

// String implements fmt.Stringer.
func (o *OptimismConfig) String() string {
	return "optimism"
}
