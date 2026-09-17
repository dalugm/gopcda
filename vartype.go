package opcda

// Automation VARTYPE constants for Variant.Type and Array.ElementType.
// Upstream generates mutable enum variables; tests keep these fixed protocol
// constants aligned without exposing mutable, binding-specific values.
const (
	VTEmpty    uint16 = 0x0000
	VTNull     uint16 = 0x0001
	VTI2       uint16 = 0x0002
	VTI4       uint16 = 0x0003
	VTR4       uint16 = 0x0004
	VTR8       uint16 = 0x0005
	VTCY       uint16 = 0x0006
	VTDate     uint16 = 0x0007
	VTBSTR     uint16 = 0x0008
	VTDispatch uint16 = 0x0009
	VTError    uint16 = 0x000a
	VTBool     uint16 = 0x000b
	VTVariant  uint16 = 0x000c
	VTUnknown  uint16 = 0x000d
	VTDecimal  uint16 = 0x000e
	VTI1       uint16 = 0x0010
	VTUI1      uint16 = 0x0011
	VTUI2      uint16 = 0x0012
	VTUI4      uint16 = 0x0013
	VTI8       uint16 = 0x0014
	VTUI8      uint16 = 0x0015
	VTInt      uint16 = 0x0016
	VTUint     uint16 = 0x0017
	VTRecord   uint16 = 0x0024
	VTArray    uint16 = 0x2000
	VTByRef    uint16 = 0x4000
)
