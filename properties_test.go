package opcda

import (
	"context"
	"encoding/binary"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/oiweiwei/go-msrpc/dcerpc"
	"github.com/oiweiwei/go-msrpc/msrpc/dcom"
	"github.com/oiweiwei/go-msrpc/msrpc/dcom/oaut"
	"github.com/oiweiwei/go-msrpc/ndr"
	props "github.com/oiweiwei/go-opcda/opc/opcda/iopcitemproperties/v0"
)

type propertyRPC struct {
	dcerpc.Conn
	available *props.QueryAvailablePropertiesResponse
	values    *props.GetItemPropertiesResponse
	calls     int
	wantIPID  *dcom.IPID
}

func TestPropertyExtendedValuesAndPartialFailure(t *testing.T) {
	values := []any{
		Currency(-12345),
		Decimal{Hi: 0xffffffff, Lo: 0xffffffffffffffff, Scale: 28},
		ErrorCode(0x80004005),
		time.Date(2026, 9, 15, 0, 0, 0, 123000000, time.UTC),
		Variant{Type: 0x4008, Value: "\u6d41\u91cf"},
		Array{
			ElementType: 12,
			Bounds:      []ArrayBound{{Lower: -2, Count: 2}},
			Values: []any{
				Variant{Type: 14, Value: Decimal{Lo: 12345, Scale: 2}},
				Variant{Type: 8, Value: "a\x00b"},
			},
		},
	}
	c := &propertyRPC{
		available: &props.QueryAvailablePropertiesResponse{That: &dcom.ORPCThat{}},
		values:    &props.GetItemPropertiesResponse{That: &dcom.ORPCThat{}, Return: 1},
	}
	for i, value := range values {
		v, err := writeVariant(value)
		if err != nil {
			t.Fatal(err)
		}
		c.available.PropertyIDs = append(c.available.PropertyIDs, uint32(i+1))
		c.available.Descriptions = append(c.available.Descriptions, "Property")
		c.available.DataTypes = append(c.available.DataTypes, v.VT)
		c.values.Data = append(c.values.Data, v)
		c.values.Errors = append(c.values.Errors, 0)
	}
	c.available.PropertyIDs = append(c.available.PropertyIDs, 101)
	c.available.Descriptions = append(c.available.Descriptions, "Description")
	c.available.DataTypes = append(c.available.DataTypes, 8)
	c.values.Data = append(c.values.Data, &oaut.Variant{})
	c.values.Errors = append(c.values.Errors, -1073479165)
	c.available.Count = uint32(len(c.available.PropertyIDs))
	c.values.Count = c.available.Count
	got, err := readProperties(t.Context(), c, nil, "Pump.Value")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(values)+1 {
		t.Fatalf("properties=%d", len(got))
	}
	for i, value := range values {
		if got[i].Error != nil || !reflect.DeepEqual(got[i].Value, value) {
			t.Fatalf("property %d got=%#v want=%#v error=%v", i, got[i].Value, value, got[i].Error)
		}
	}
	if got[len(values)].Error == nil {
		t.Fatal("lost property HRESULT failure")
	}
}

func TestUnsupportedCOMPropertyPreservesSiblingAndHRESULT(t *testing.T) {
	good, err := writeVariant(float32(12.5))
	if err != nil {
		t.Fatal(err)
	}
	unknown := &oaut.Variant{
		Size:     3,
		VT:       13,
		VarUnion: &oaut.Variant_VarUnion{Value: &oaut.Variant_VarUnion_IUnknown{}},
	}
	c := &propertyRPC{
		available: &props.QueryAvailablePropertiesResponse{
			That:         &dcom.ORPCThat{},
			Count:        3,
			PropertyIDs:  []uint32{1, 2, 3},
			Descriptions: []string{"Good", "Object", "Failed"},
			DataTypes:    []uint16{4, 13, 13},
		},
		values: &props.GetItemPropertiesResponse{
			That:   &dcom.ORPCThat{},
			Count:  3,
			Data:   []*oaut.Variant{good, unknown, unknown},
			Errors: []int32{0, 0, -1073479165},
			Return: 1,
		},
	}
	got, err := readProperties(t.Context(), c, nil, "Pump.Value")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Value != float32(12.5) || got[0].Error != nil ||
		got[1].Error == nil {
		t.Fatalf("properties=%+v", got)
	}
	var hr *HRESULTError
	if !errors.As(got[2].Error, &hr) {
		t.Fatalf("lost per-item HRESULT: %v", got[2].Error)
	}
}

func (c *propertyRPC) Invoke(
	ctx context.Context,
	op dcerpc.Operation,
	opts ...dcerpc.CallOption,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.wantIPID != nil {
		got, _ := dcom.HasIPID(opts)
		if got == nil || !got.UUID().Equals(c.wantIPID.UUID()) {
			return errors.New("wrong property IPID")
		}
	}
	request, err := ndr.Marshal(ndr.MarshalNDRFunc(op.MarshalNDRRequest))
	if err != nil {
		return err
	}
	if op.OpNum() == 4 {
		var req props.GetItemPropertiesRequest
		if err := ndr.Unmarshal(request, &req); err != nil {
			return err
		}
		if req.ItemID != "Pump.Value" || len(req.PropertyIDs) != len(c.available.PropertyIDs) {
			return errors.New("wrong property request")
		}
		for i, id := range req.PropertyIDs {
			if id != c.available.PropertyIDs[i] {
				return errors.New("wrong property order")
			}
		}
	}
	c.calls++
	var response ndr.Marshaler
	switch op.OpNum() {
	case 3:
		response = c.available
	case 4:
		response = c.values
	default:
		return errors.New("unexpected operation")
	}
	wire, err := ndr.Marshal(ndr.MarshalNDRFunc(func(ctx context.Context, w ndr.Writer) error {
		return response.MarshalNDR(ctx, bindingsWriter{w})
	}))
	if err != nil {
		return err
	}
	return ndr.Unmarshal(wire, ndr.UnmarshalNDRFunc(op.UnmarshalNDRResponse))
}

func TestItemPropertiesPreserveDescriptionAndPartialErrors(t *testing.T) {
	for _, description := range []string{"", "\u6d41\u91cf\u7ed9\u5b9a", "a\x00b"} {
		v, err := writeVariant(description)
		if err != nil {
			t.Fatal(err)
		}
		c := &propertyRPC{
			available: &props.QueryAvailablePropertiesResponse{
				That:         &dcom.ORPCThat{},
				Count:        2,
				PropertyIDs:  []uint32{101, 100},
				Descriptions: []string{"Item Description", "EU Units"},
				DataTypes:    []uint16{8, 8},
			},
			values: &props.GetItemPropertiesResponse{
				That:   &dcom.ORPCThat{},
				Count:  2,
				Data:   []*oaut.Variant{v, {}},
				Errors: []int32{0, -1073479165},
				Return: 1,
			},
		}
		c.wantIPID = &dcom.IPID{Data1: 123}
		got, err := readProperties(t.Context(), c, c.wantIPID, "Pump.Value")
		if err != nil || len(got) != 2 {
			t.Fatalf("properties=%+v err=%v", got, err)
		}
		if got[0].ID != 101 || got[0].Value != description || got[0].Error != nil ||
			got[0].Name != "Item Description" ||
			got[0].DataType != 8 {
			t.Fatalf("description=%+v", got[0])
		}
		if got[1].Error == nil || got[1].Value != nil {
			t.Fatalf("failed property=%+v", got[1])
		}
	}
}

func TestPropertyOperationFailures(t *testing.T) {
	for _, stage := range []string{"query", "read"} {
		c := &propertyRPC{
			available: &props.QueryAvailablePropertiesResponse{
				That:         &dcom.ORPCThat{},
				Count:        1,
				PropertyIDs:  []uint32{101},
				Descriptions: []string{"Description"},
				DataTypes:    []uint16{8},
			},
			values: &props.GetItemPropertiesResponse{
				That:   &dcom.ORPCThat{},
				Return: -2147467259,
			},
		}
		if stage == "query" {
			c.available = &props.QueryAvailablePropertiesResponse{
				That:   &dcom.ORPCThat{},
				Return: -2147467262,
			}
		}
		result, err := readProperties(t.Context(), c, nil, "Pump.Value")
		var hr *HRESULTError
		if result != nil || !errors.As(err, &hr) {
			t.Fatalf("%s result=%v error=%v", stage, result, err)
		}
	}
}

func TestPropertyValuesRejectTruncatedAndMismatchedResponses(t *testing.T) {
	v, err := writeVariant("\u6d41\u91cf")
	if err != nil {
		t.Fatal(err)
	}
	wire, err := ndr.Marshal(ndr.MarshalNDRFunc(func(ctx context.Context, w ndr.Writer) error {
		return (&props.GetItemPropertiesResponse{That: &dcom.ORPCThat{}, Count: 1, Data: []*oaut.Variant{v}, Errors: []int32{0}}).MarshalNDR(
			ctx,
			bindingsWriter{w},
		)
	}))
	if err != nil {
		t.Fatal(err)
	}
	for n := range len(wire) {
		if err := ndr.Unmarshal(wire[:n], &propertyValuesResponse{count: 1}); err == nil {
			t.Fatalf("accepted value truncation %d", n)
		}
	}
	if err := ndr.Unmarshal(wire, &propertyValuesResponse{count: 1}); err != nil {
		t.Fatal(err)
	}
	if err := ndr.Unmarshal(wire, &propertyValuesResponse{count: 2}); err == nil {
		t.Fatal("accepted wrong value count")
	}
	missing, err := ndr.Marshal(&props.GetItemPropertiesResponse{That: &dcom.ORPCThat{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := ndr.Unmarshal(missing, &propertyValuesResponse{count: 1}); err == nil {
		t.Fatal("accepted missing value arrays")
	}
}

type propertiesConnection struct {
	connection
	query func(context.Context, string) ([]ItemProperty, error)
}

func (c *propertiesConnection) itemProperties(
	ctx context.Context,
	id string,
) ([]ItemProperty, error) {
	return c.query(ctx, id)
}

func TestServerItemPropertiesValidationAndCancellation(t *testing.T) {
	lifetime, cancel := context.WithCancel(t.Context())
	defer cancel()
	c := &propertiesConnection{query: func(ctx context.Context, id string) ([]ItemProperty, error) {
		cancel()
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	s := &Server{ctx: lifetime, conn: c}
	for _, id := range []string{"", "A\x00B", "\xff"} {
		if _, err := s.ItemProperties(t.Context(), id); err == nil {
			t.Fatalf("accepted invalid id %q", id)
		}
	}
	if _, err := s.ItemProperties(t.Context(), "Pump.Value"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
	s.closed = true
	if _, err := s.ItemProperties(t.Context(), "Pump.Value"); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed=%v", err)
	}
}

func TestItemPropertiesEmptySkipsValueRead(t *testing.T) {
	c := &propertyRPC{available: &props.QueryAvailablePropertiesResponse{That: &dcom.ORPCThat{}}}
	got, err := readProperties(t.Context(), c, nil, "Pump.Value")
	if err != nil || len(got) != 0 || c.calls != 1 {
		t.Fatalf("properties=%v calls=%d err=%v", got, c.calls, err)
	}
}

func TestPropertyWireRejectsTruncationAndExcessCounts(t *testing.T) {
	wire, err := ndr.Marshal(
		&props.QueryAvailablePropertiesResponse{
			That:         &dcom.ORPCThat{},
			Count:        1,
			PropertyIDs:  []uint32{101},
			Descriptions: []string{"Description"},
			DataTypes:    []uint16{8},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	for n := range len(wire) {
		if err := ndr.Unmarshal(wire[:n], &availablePropertiesResponse{}); err == nil {
			t.Fatalf("accepted truncation %d", n)
		}
	}
	if err := ndr.Unmarshal(wire, &availablePropertiesResponse{}); err != nil {
		t.Fatal(err)
	}
	// ORPCTHAT (8), count (4), property-array pointer (4), conformant count.
	binary.LittleEndian.PutUint32(wire[16:], 0xffffffff)
	if err := ndr.Unmarshal(wire, &availablePropertiesResponse{}); err == nil {
		t.Fatal("accepted excessive array count")
	}
}

func TestPropertyRequestPreservesUnicodeItemID(t *testing.T) {
	wire, err := ndr.Marshal(&propertyRequest{id: "A\U0001f680", ids: []uint32{101}})
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint32(wire[32:]) != 4 || binary.LittleEndian.Uint32(wire[40:]) != 4 {
		t.Fatalf("bad UTF-16 counts: %x", wire)
	}
	var req props.GetItemPropertiesRequest
	if err := ndr.Unmarshal(wire, &req); err != nil {
		t.Fatal(err)
	}
	if req.ItemID != "A\U0001f680" || len(req.PropertyIDs) != 1 || req.PropertyIDs[0] != 101 {
		t.Fatalf("request=%+v", req)
	}
}
