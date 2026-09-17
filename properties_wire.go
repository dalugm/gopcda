package opcda

import (
	"context"
	"errors"
	"fmt"

	"github.com/oiweiwei/go-msrpc/msrpc/dcom/oaut"
	"github.com/oiweiwei/go-msrpc/ndr"
	props "github.com/oiweiwei/go-opcda/opc/opcda/iopcitemproperties/v0"
)

type propertyRequest struct {
	id  string
	ids []uint32
}

func (r *propertyRequest) MarshalNDR(ctx context.Context, w ndr.Writer) error {
	if r.ids == nil {
		return (&props.QueryAvailablePropertiesRequest{This: orpcThis(), ItemID: r.id}).MarshalNDR(
			ctx,
			w,
		)
	}
	return (&props.GetItemPropertiesRequest{This: orpcThis(), ItemID: r.id, PropertyIDs: r.ids}).MarshalNDR(
		ctx,
		w,
	)
}

type availablePropertiesResponse struct {
	props.QueryAvailablePropertiesResponse
}

func (r *availablePropertiesResponse) UnmarshalNDR(ctx context.Context, w ndr.Reader) error {
	*r = availablePropertiesResponse{}
	if err := r.QueryAvailablePropertiesResponse.UnmarshalNDR(
		ctx,
		propertyReader{Reader: w, count: -1},
	); err != nil {
		return err
	}
	if r.Return < 0 {
		return nil
	}
	n := int(r.Count)
	if n > maxBatchItems || len(r.PropertyIDs) != n || len(r.Descriptions) != n ||
		len(r.DataTypes) != n {
		return errors.New("inconsistent property metadata counts")
	}
	seen := make(map[uint32]bool, n)
	for _, id := range r.PropertyIDs {
		if seen[id] {
			return fmt.Errorf("duplicate property ID %d", id)
		}
		seen[id] = true
	}
	return nil
}

type propertyValuesResponse struct {
	props.GetItemPropertiesResponse
	count int
}

func (r *propertyValuesResponse) UnmarshalNDR(ctx context.Context, w ndr.Reader) error {
	r.GetItemPropertiesResponse = props.GetItemPropertiesResponse{}
	if err := r.GetItemPropertiesResponse.UnmarshalNDR(
		ctx,
		propertyReader{Reader: w, count: r.count},
	); err != nil {
		return err
	}
	if r.Return >= 0 && (len(r.Data) != r.count || len(r.Errors) != r.count) {
		return errors.New("inconsistent property value counts")
	}
	return nil
}

// Bound arrays before generated decoders allocate them. Enumeration has no
// request count; its three arrays are cross-checked after decoding instead.
type propertyReader struct {
	ndr.Reader
	count int
}

func (r propertyReader) ReadPointer(
	ptr ndr.Pointer,
	setter func(any),
	bodies ...ndr.Unmarshaler,
) error {
	if value, ok := ptr.(**oaut.Variant); ok {
		return r.Reader.ReadPointer(
			ptr,
			setter,
			ndr.UnmarshalNDRFunc(func(ctx context.Context, target ndr.Reader) error {
				*value = &oaut.Variant{}
				return unmarshalReadVariant(ctx, target, *value)
			}),
		)
	}
	array := false
	switch ptr.(type) {
	case *[]uint32, *[]uint16, *[]string, *[]int32, *[]*oaut.Variant:
		array = true
	}
	wrapped := make([]ndr.Unmarshaler, len(bodies))
	for i, body := range bodies {
		wrapped[i] = ndr.UnmarshalNDRFunc(func(ctx context.Context, target ndr.Reader) error {
			reader := propertyReader{Reader: target, count: r.count}
			if array {
				return body.UnmarshalNDR(
					ctx,
					&propertyArrayReader{propertyReader: reader, first: true},
				)
			}
			return body.UnmarshalNDR(ctx, reader)
		})
	}
	return r.Reader.ReadPointer(ptr, setter, wrapped...)
}

type propertyArrayReader struct {
	propertyReader
	first bool
}

func (r *propertyArrayReader) ReadSize(size *uint64) error {
	if err := r.Reader.ReadSize(size); err != nil {
		return err
	}
	if r.first {
		r.first = false
		if *size > maxBatchItems || (r.count >= 0 && *size != uint64(r.count)) {
			return fmt.Errorf("invalid property array count %d", *size)
		}
	}
	return nil
}
