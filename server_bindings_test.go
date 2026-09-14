package opcda

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/oiweiwei/go-msrpc/msrpc/dcom"
	"github.com/oiweiwei/go-msrpc/msrpc/dtyp"
	"github.com/oiweiwei/go-msrpc/ndr"
	binding "github.com/oiweiwei/go-opcda/opc/opcda"
	iopcserver "github.com/oiweiwei/go-opcda/opc/opcda/iopcserver/v0"
)

func TestAddGroupBindingsPreserveUnicodeNameAndOptionalPointers(t *testing.T) {
	bias, deadband := int32(-60), float32(12.5)
	for _, tt := range []struct {
		name     string
		bias     *int32
		deadband *float32
		nonNull  bool
	}{
		{name: "nil pointers"},
		{name: "non-null pointers", bias: &bias, deadband: &deadband, nonNull: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			const groupName = "\u03a9\U0001f642"
			request := &addGroupReq{
				Name:            groupName,
				Active:          1,
				ReqUpdateRate:   500,
				ClientHandle:    7,
				TimeBias:        tt.bias,
				PercentDeadband: tt.deadband,
				RIID:            iopcItemMgtIID,
			}
			wire, err := ndr.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}

			// The name is one BMP code point, one surrogate pair and a NUL.
			for _, offset := range []int{32, 40} {
				if got := binary.LittleEndian.Uint32(wire[offset:]); got != 4 {
					t.Fatalf("UTF-16 count at offset %d = %d, want 4: %x", offset, got, wire)
				}
			}
			if got := binary.LittleEndian.Uint32(wire[36:]); got != 0 {
				t.Fatalf("UTF-16 offset = %d, want 0: %x", got, wire)
			}

			// pTimeBias and pPercentDeadband are optional unique pointers. The
			// generated scalar fields cannot represent nil, so the adapter must
			// preserve the private request's pointer state.
			for _, offset := range []int{64, 68} {
				if got := binary.LittleEndian.Uint32(wire[offset:]); (got != 0) != tt.nonNull {
					t.Fatalf(
						"pointer at offset %d = %#x, non-null want %t: %x",
						offset,
						got,
						tt.nonNull,
						wire,
					)
				}
			}

			var decoded iopcserver.AddGroupRequest
			if err := ndr.Unmarshal(wire, &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded.Name != groupName || !decoded.Active || decoded.RequestedUpdateRate != 500 ||
				decoded.ClientGroup != 7 {
				t.Fatalf("decoded generated request: %+v", decoded)
			}
			if tt.nonNull && (decoded.TimeBias != bias || decoded.PercentDeadband != deadband) {
				t.Fatalf("decoded generated pointer values: %+v", decoded)
			}
		})
	}
}

func TestServerBindingsDecodeGeneratedResponses(t *testing.T) {
	groupWire, err := ndr.Marshal(&iopcserver.AddGroupResponse{
		That:              &dcom.ORPCThat{},
		ServerGroup:       123,
		RevisedUpdateRate: 1000,
		Unknown:           &dcom.Unknown{Data: []byte{1, 2, 3, 4}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var group addGroupResp
	if err := ndr.Unmarshal(groupWire, &group); err != nil {
		t.Fatal(err)
	}
	if group.ServerHandle != 123 || group.RevisedRate != 1000 || group.GroupIface == nil ||
		string(group.GroupIface.Data) != "\x01\x02\x03\x04" {
		t.Fatalf("decoded AddGroup response: %+v", group)
	}

	statusResponse := &iopcserver.GetStatusResponse{
		That: &dcom.ORPCThat{},
		ServerStatus: &binding.ServerStatus{
			StartTime:         &dtyp.Filetime{LowDateTime: 0x11111111, HighDateTime: 0x22222222},
			CurrentTime:       &dtyp.Filetime{LowDateTime: 0x33333333, HighDateTime: 0x44444444},
			LastUpdateTime:    &dtyp.Filetime{LowDateTime: 0x55555555, HighDateTime: 0x66666666},
			ServerState:       1,
			GroupCount:        2,
			Bandwidth:         100,
			MajorVersion:      3,
			MinorVersion:      2,
			BuildNumber:       7,
			VendorInformation: "\u03a9\u00e9",
		},
	}
	statusWire, err := ndr.Marshal(
		ndr.MarshalNDRFunc(func(ctx context.Context, w ndr.Writer) error {
			return statusResponse.MarshalNDR(ctx, bindingsWriter{w})
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	var status getStatusResp
	if err := ndr.Unmarshal(statusWire, &status); err != nil {
		t.Fatal(err)
	}
	if status.StatusData == nil || status.StatusData.Times != [6]uint32{
		0x11111111, 0x22222222, 0x33333333, 0x44444444, 0x55555555, 0x66666666,
	} || status.StatusData.Vendor != "\u03a9\u00e9" {
		t.Fatalf("decoded GetStatus response: %+v", status)
	}
}
