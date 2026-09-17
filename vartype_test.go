package opcda

import (
	"testing"

	"github.com/oiweiwei/go-msrpc/msrpc/dcom/oaut"
)

func TestVARTYPEConstantsMatchBindings(t *testing.T) {
	for _, tt := range []struct {
		local    uint16
		upstream oaut.VarEnum
	}{
		{VTEmpty, oaut.VarEmpty},
		{VTNull, oaut.VarNull},
		{VTI1, oaut.VarEnumI1},
		{VTUI1, oaut.VarEnumUI1},
		{VTI2, oaut.VarEnumI2},
		{VTUI2, oaut.VarEnumUI2},
		{VTI4, oaut.VarEnumI4},
		{VTUI4, oaut.VarEnumUI4},
		{VTI8, oaut.VarEnumI8},
		{VTUI8, oaut.VarEnumUI8},
		{VTR4, oaut.VarEnumR4},
		{VTR8, oaut.VarEnumR8},
		{VTCY, oaut.VarCurrency},
		{VTDate, oaut.VarEnumDate},
		{VTBSTR, oaut.VarEnumString},
		{VTDispatch, oaut.VarEnumDispatch},
		{VTError, oaut.VarEnumError},
		{VTBool, oaut.VarEnumBool},
		{VTVariant, oaut.VarEnumVariant},
		{VTUnknown, oaut.VarEnumUnknown},
		{VTDecimal, oaut.VarEnumDecimal},
		{VTInt, oaut.VarEnumInt},
		{VTUint, oaut.VarEnumUint},
		{VTRecord, oaut.VarEnumRecord},
		{VTArray, oaut.VarEnumArray},
		{VTByRef, oaut.VarEnumByref},
	} {
		if tt.local != uint16(tt.upstream) {
			t.Fatalf("local 0x%04x differs from upstream %v", tt.local, tt.upstream)
		}
	}
	const reference = VTR8 | VTArray | VTByRef
	if reference != 0x6005 {
		t.Fatalf("array reference tag = 0x%04x", reference)
	}
}
