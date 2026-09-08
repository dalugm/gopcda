package opcda

import (
	"context"
	"fmt"

	"github.com/oiweiwei/go-msrpc/msrpc/dcom"
	"github.com/oiweiwei/go-msrpc/msrpc/dcom/oaut"
	"github.com/oiweiwei/go-msrpc/ndr"
)

type addOneRequest struct{ id string }

func (r *addOneRequest) MarshalNDR(ctx context.Context, w ndr.Writer) error {
	if err := writeORPC(ctx, w); err != nil {
		return err
	}
	if err := w.WriteData(uint32(1)); err != nil {
		return err
	}
	if err := w.WriteSize(1); err != nil {
		return err
	}
	for _, value := range []string{"", r.id} {
		text := value
		body := ndr.MarshalNDRFunc(
			func(ctx context.Context, w ndr.Writer) error { return ndr.WriteUTF16NString(ctx, w, text) },
		)
		if err := w.WritePointer(&text, body); err != nil {
			return err
		}
	}
	// OPCITEMDEF: active, client handle, blob size, null blob, VT_EMPTY, reserved.
	for _, v := range []any{uint32(1), uint32(1), uint32(0), uint32(0), uint16(0), uint16(0)} {
		if err := w.WriteData(v); err != nil {
			return err
		}
	}
	return w.WriteDeferred()
}

func readSingleArray(ctx context.Context, w ndr.Reader, present *bool, body ndr.Unmarshaler) error {
	f := ndr.UnmarshalNDRFunc(func(ctx context.Context, w ndr.Reader) error {
		*present = true
		var count uint64
		if err := w.ReadSize(&count); err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("expected one result, received %d", count)
		}
		return body.UnmarshalNDR(ctx, w)
	})
	if err := w.ReadPointer(present, func(v any) { *present = *v.(*bool) }, f); err != nil {
		return err
	}
	return w.ReadDeferred()
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
	if err := readORPC(ctx, w); err != nil {
		return err
	}
	body := ndr.UnmarshalNDRFunc(func(ctx context.Context, w ndr.Reader) error {
		var reserved uint16
		var blobSize uint32
		for _, v := range []any{&r.handle, &r.canonical, &reserved, &r.rights, &blobSize} {
			if err := w.ReadData(v); err != nil {
				return err
			}
		}
		var blob []byte
		f := ndr.UnmarshalNDRFunc(func(ctx context.Context, w ndr.Reader) error {
			var count uint64
			if err := w.ReadSize(&count); err != nil {
				return err
			}
			if count != uint64(blobSize) || count > uint64(w.Len()) {
				return fmt.Errorf("invalid item blob length")
			}
			blob = make([]byte, int(count))
			for i := range blob {
				if err := w.ReadData(&blob[i]); err != nil {
					return err
				}
			}
			return nil
		})
		return w.ReadPointer(&blob, func(v any) { blob = *v.(*[]byte) }, f)
	})
	if err := readSingleArray(ctx, w, &r.hasResult, body); err != nil {
		return err
	}
	if err := readSingleArray(
		ctx,
		w,
		&r.hasError,
		ndr.UnmarshalNDRFunc(
			func(ctx context.Context, w ndr.Reader) error { return w.ReadData(&r.itemError) },
		),
	); err != nil {
		return err
	}
	return w.ReadData(&r.hresult)
}

func (r *addOneResponse) check() error {
	if r.hresult < 0 {
		return hresultError("AddItems", "", r.hresult)
	}
	if !r.hasError {
		return fmt.Errorf("AddItems: missing item HRESULT")
	}
	if r.itemError < 0 {
		return hresultError("AddItems", "", r.itemError)
	}
	if !r.hasResult {
		return fmt.Errorf("AddItems: missing result")
	}
	return nil
}

type readOneRequest struct{ handle uint32 }

func (r *readOneRequest) MarshalNDR(ctx context.Context, w ndr.Writer) error {
	if err := writeORPC(ctx, w); err != nil {
		return err
	}
	// OPC_DS_DEVICE=2, count=1, conformant array of one server handle.
	for _, v := range []any{uint16(2), uint32(1), uint32(1), r.handle} {
		if err := w.WriteData(v); err != nil {
			return err
		}
	}
	return nil
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
	if err := readORPC(ctx, w); err != nil {
		return err
	}
	body := ndr.UnmarshalNDRFunc(func(ctx context.Context, w ndr.Reader) error {
		var reserved uint16
		for _, v := range []any{&r.client, &r.timeLow, &r.timeHigh, &r.quality, &reserved} {
			if err := w.ReadData(v); err != nil {
				return err
			}
		}
		variant := ndr.UnmarshalNDRFunc(func(ctx context.Context, w ndr.Reader) error {
			r.variant = &oaut.Variant{}
			return unmarshalReadVariant(ctx, w, r.variant)
		})
		return w.ReadPointer(&r.variant, func(v any) { r.variant = *v.(**oaut.Variant) }, variant)
	})
	if err := readSingleArray(ctx, w, &r.hasState, body); err != nil {
		return err
	}
	if err := readSingleArray(
		ctx,
		w,
		&r.hasError,
		ndr.UnmarshalNDRFunc(
			func(ctx context.Context, w ndr.Reader) error { return w.ReadData(&r.itemError) },
		),
	); err != nil {
		return err
	}
	return w.ReadData(&r.hresult)
}

func (r *readOneResponse) result(id string) (*ReadResult, error) {
	if r.hresult < 0 {
		return nil, hresultError("Read", "", r.hresult)
	}
	if !r.hasError {
		return nil, fmt.Errorf("Read: missing item HRESULT")
	}
	if r.itemError < 0 {
		return nil, hresultError("Read", id, r.itemError)
	}
	if !r.hasState || r.variant == nil {
		return nil, fmt.Errorf("Read: missing item state or VARIANT")
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
	switch v.VT {
	case 0, 1:
		return nil, nil
	case 2, 3, 4, 5, 17, 18, 19, 20, 21, 22, 23:
		return v.VarUnion.GetValue(), nil
	case 8:
		value, ok := v.VarUnion.GetValue().(*oaut.String)
		if !ok {
			return nil, fmt.Errorf("invalid VARIANT_BSTR")
		}
		if value == nil {
			return "", nil
		}
		return value.Data, nil
	case 16:
		value, ok := v.VarUnion.GetValue().(uint8)
		if !ok {
			return nil, fmt.Errorf("invalid VARIANT_I1")
		}
		return int8(value), nil
	case 11:
		value, ok := v.VarUnion.GetValue().(int16)
		if !ok {
			return nil, fmt.Errorf("invalid VARIANT_BOOL")
		}
		return value != 0, nil
	default:
		return nil, fmt.Errorf("unsupported VARIANT type 0x%04x", v.VT)
	}
}

type removeReadGroupRequest struct{ handle uint32 }

func (r *removeReadGroupRequest) MarshalNDR(ctx context.Context, w ndr.Writer) error {
	if err := writeORPC(ctx, w); err != nil {
		return err
	}
	if err := w.WriteData(r.handle); err != nil {
		return err
	}
	return w.WriteData(uint32(0))
}

type hresultResponse struct{ hresult int32 }

func (r *hresultResponse) UnmarshalNDR(ctx context.Context, w ndr.Reader) error {
	if err := readORPC(ctx, w); err != nil {
		return err
	}
	return w.ReadData(&r.hresult)
}

// AddGroup's ppUnk is a unique MInterfacePointer, not an inline struct.
func readGroupResponse(ctx context.Context, w ndr.Reader, r *addGroupResp) error {
	*r = addGroupResp{ORPCThat: &dcom.ORPCThat{}}
	if err := r.ORPCThat.UnmarshalNDR(ctx, w); err != nil {
		return err
	}
	if err := w.ReadDeferred(); err != nil {
		return err
	}
	if err := w.ReadData(&r.ServerHandle); err != nil {
		return err
	}
	if err := w.ReadData(&r.RevisedRate); err != nil {
		return err
	}
	body := ndr.UnmarshalNDRFunc(func(ctx context.Context, w ndr.Reader) error {
		r.GroupIface = &dcom.InterfacePointer{}
		return r.GroupIface.UnmarshalNDR(ctx, w)
	})
	if err := w.ReadPointer(
		&r.GroupIface,
		func(v any) { r.GroupIface = *v.(**dcom.InterfacePointer) },
		body,
	); err != nil {
		return err
	}
	if err := w.ReadDeferred(); err != nil {
		return err
	}
	return w.ReadData(&r.Return)
}
