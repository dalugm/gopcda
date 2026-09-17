package opcda

import (
	"context"
	"errors"
	"fmt"

	"github.com/oiweiwei/go-msrpc/msrpc/dcom/oaut"
	"github.com/oiweiwei/go-msrpc/ndr"
	serverbinding "github.com/oiweiwei/go-opcda/opc/opcda/iopcserver/v0"
)

type addOneRequest struct{ id string }

func (r *addOneRequest) MarshalNDR(ctx context.Context, w ndr.Writer) error {
	return (&batchAddRequest{ids: []string{r.id}, clients: []uint32{1}}).MarshalNDR(ctx, w)
}

type addOneResponse struct {
	handle              uint32
	canonical           uint16
	rights              uint32
	itemError, hresult  int32
	hasResult, hasError bool
}

func (r *addOneResponse) UnmarshalNDR(ctx context.Context, w ndr.Reader) error {
	*r = addOneResponse{}
	response := &batchAddResponse{count: 1}
	if err := response.UnmarshalNDR(ctx, w); err != nil {
		return err
	}
	if len(response.items) == 1 {
		*r = response.items[0]
		r.hasResult = true
	}
	if len(response.errors) == 1 {
		r.itemError = response.errors[0]
		r.hasError = true
	}
	r.hresult = response.hresult
	return nil
}

func (r *addOneResponse) check() error {
	if r.hresult < 0 {
		return hresultError("AddItems", "", r.hresult)
	}
	if !r.hasError {
		return errors.New("AddItems: missing item HRESULT")
	}
	if r.itemError < 0 {
		return hresultError("AddItems", "", r.itemError)
	}
	if !r.hasResult {
		return errors.New("AddItems: missing result")
	}
	return nil
}

type readOneRequest struct{ handle uint32 }

func (r *readOneRequest) MarshalNDR(ctx context.Context, w ndr.Writer) error {
	return (&batchReadRequest{handles: []uint32{r.handle}}).MarshalNDR(ctx, w)
}

type readOneResponse struct {
	client             uint32
	timeLow, timeHigh  uint32
	quality            uint16
	variant            *oaut.Variant
	itemError, hresult int32
	hasState, hasError bool
}

func (r *readOneResponse) UnmarshalNDR(ctx context.Context, w ndr.Reader) error {
	*r = readOneResponse{}
	response := &batchReadResponse{count: 1}
	if err := response.UnmarshalNDR(ctx, w); err != nil {
		return err
	}
	if len(response.states) == 1 {
		*r = response.states[0]
		r.hasState = true
	}
	if len(response.errors) == 1 {
		r.itemError = response.errors[0]
		r.hasError = true
	}
	r.hresult = response.hresult
	return nil
}

func (r *readOneResponse) result(id string) (*ReadResult, error) {
	if r.hresult < 0 {
		return nil, hresultError("Read", "", r.hresult)
	}
	if !r.hasError {
		return nil, errors.New("Read: missing item HRESULT")
	}
	if r.itemError < 0 {
		return nil, hresultError("Read", id, r.itemError)
	}
	if !r.hasState || r.variant == nil {
		return nil, errors.New("Read: missing item state or VARIANT")
	}
	value, err := scalarValue(r.variant)
	if err != nil {
		return nil, err
	}
	ticks := uint64(r.timeLow) | uint64(r.timeHigh)<<32
	return &ReadResult{
		ItemID:            id,
		Value:             value,
		Quality:           int16(r.quality),
		SourceTimestampMs: int64(ticks/10000) - 11644473600000,
	}, nil
}

func scalarValue(v *oaut.Variant) (any, error) {
	return scalarValueDepth(v, 0)
}

func scalarValueDepth(v *oaut.Variant, depth int) (any, error) {
	if depth > 32 {
		return nil, errors.New("VARIANT nesting exceeds 32")
	}
	if v == nil || v.VarUnion == nil {
		return nil, errors.New("missing VARIANT value")
	}
	if v.VT&VTByRef != 0 {
		base := *v
		base.VT &^= VTByRef
		if base.VT == VTVariant {
			inner, ok := v.VarUnion.GetValue().(*oaut.Variant)
			if !ok || inner == nil {
				return nil, errors.New("missing BYREF VARIANT")
			}
			value, err := scalarValueDepth(inner, depth+1)
			if inner.VT&VTByRef == 0 {
				value = Variant{Type: inner.VT, Value: value}
			}
			return Variant{Type: v.VT, Value: value}, err
		}
		value, err := scalarValueDepth(&base, depth+1)
		return Variant{Type: v.VT, Value: value}, err
	}
	if v.VT&VTArray != 0 {
		a, ok := v.VarUnion.GetValue().(*oaut.SafeArray)
		if !ok {
			return nil, errors.New("invalid SAFEARRAY")
		}
		return readArray(a, v.VT&^VTArray, depth+1)
	}
	switch v.VT {
	case VTEmpty, VTNull:
		return nil, nil
	case VTI2, VTI4, VTR4, VTR8, VTUI1, VTUI2, VTUI4, VTI8, VTUI8, VTInt, VTUint:
		return v.VarUnion.GetValue(), nil
	case VTDate:
		value, ok := v.VarUnion.GetValue().(float64)
		if !ok {
			return nil, errors.New("invalid VARIANT_DATE")
		}
		return automationDate(value)
	case VTCY:
		value, ok := v.VarUnion.GetValue().(*oaut.Currency)
		if !ok || value == nil {
			return nil, errors.New("invalid VARIANT_CY")
		}
		return Currency(value.Int64), nil
	case VTDecimal:
		value, ok := v.VarUnion.GetValue().(*oaut.Decimal)
		if !ok || value == nil || value.Scale > 28 || (value.Sign != 0 && value.Sign != 0x80) {
			return nil, errors.New("invalid VARIANT_DECIMAL")
		}
		return Decimal{
			Hi:       value.Hi32,
			Lo:       value.Lo64,
			Scale:    value.Scale,
			Negative: value.Sign == 0x80,
		}, nil
	case VTError:
		value, ok := v.VarUnion.GetValue().(int32)
		if !ok {
			return nil, errors.New("invalid VARIANT_ERROR")
		}
		return ErrorCode(uint32(value)), nil
	case VTBSTR:
		value, ok := v.VarUnion.GetValue().(*oaut.String)
		if !ok {
			return nil, errors.New("invalid VARIANT_BSTR")
		}
		if value == nil {
			return "", nil
		}
		return value.Data, nil
	case VTI1:
		value, ok := v.VarUnion.GetValue().(uint8)
		if !ok {
			return nil, errors.New("invalid VARIANT_I1")
		}
		return int8(value), nil
	case VTBool:
		value, ok := v.VarUnion.GetValue().(int16)
		if !ok {
			return nil, errors.New("invalid VARIANT_BOOL")
		}
		return value != 0, nil
	default:
		return nil, fmt.Errorf("unsupported VARIANT type 0x%04x", v.VT)
	}
}

type removeReadGroupRequest struct{ handle uint32 }

func (r *removeReadGroupRequest) MarshalNDR(ctx context.Context, w ndr.Writer) error {
	return (&serverbinding.RemoveGroupRequest{This: orpcThis(), ServerGroup: r.handle}).MarshalNDR(
		ctx,
		w,
	)
}

type hresultResponse struct{ hresult int32 }

func (r *hresultResponse) UnmarshalNDR(ctx context.Context, w ndr.Reader) error {
	response := &serverbinding.RemoveGroupResponse{}
	if err := response.UnmarshalNDR(ctx, w); err != nil {
		return err
	}
	r.hresult = response.Return
	return nil
}
