package opcda

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/oiweiwei/go-msrpc/dcerpc"
	"github.com/oiweiwei/go-msrpc/msrpc/dcom"
	"github.com/oiweiwei/go-msrpc/ndr"
	binding "github.com/oiweiwei/go-opcda/opc/opcda"
	itemmgt "github.com/oiweiwei/go-opcda/opc/opcda/iopcitemmgt/v0"
	syncio "github.com/oiweiwei/go-opcda/opc/opcda/iopcsyncio/v0"
)

// Only the transport is replaced. Published clients decode real NDR responses
// so dependency upgrades must preserve call status and per-item results.
type upstreamResponseConn struct {
	dcerpc.Conn
	wire []byte
	err  error
}

func (c *upstreamResponseConn) Bind(context.Context, ...dcerpc.Option) (dcerpc.Conn, error) {
	return c, nil
}

func (c *upstreamResponseConn) Invoke(
	ctx context.Context,
	op dcerpc.Operation,
	_ ...dcerpc.CallOption,
) error {
	if c.err != nil {
		return c.err
	}
	return op.UnmarshalNDRResponse(ctx, ndr.NDR20(c.wire))
}

func (*upstreamResponseConn) Error(_ context.Context, code any) error {
	return fmt.Errorf("server status %v", code)
}

func TestUpstreamClientsPreservePartialResults(t *testing.T) {
	transportErr := errors.New("transport failed")
	for _, tc := range []struct {
		name      string
		status    int32
		transport error
		wantErr   bool
	}{
		{"success", 0, nil, false},
		{"partial", 1, nil, false},
		{"failure", -2147467259, nil, true},
		{"transport", 0, transportErr, true},
	} {
		for _, method := range []string{"Read", "Write", "AddItems"} {
			t.Run(method+"/"+tc.name, func(t *testing.T) {
				itemErrors := []int32{0, -1073479673}
				var response ndr.Marshaler
				switch method {
				case "Read":
					response = &syncio.ReadResponse{
						Count:      2,
						Return:     tc.status,
						Errors:     itemErrors,
						ItemValues: []*binding.ItemState{{Client: 42, Quality: 192}, {Client: 43}},
					}
				case "Write":
					response = &syncio.WriteResponse{
						Count:  2,
						Return: tc.status,
						Errors: itemErrors,
					}
				case "AddItems":
					response = &itemmgt.AddItemsResponse{
						Count:      2,
						Return:     tc.status,
						Errors:     itemErrors,
						AddResults: []*binding.ItemResult{{Server: 42}, {Server: 43}},
					}
				}
				wire, err := ndr.Marshal(response)
				if err != nil {
					t.Fatal(err)
				}
				conn := &upstreamResponseConn{wire: wire, err: tc.transport}
				ctx := t.Context()
				var status int32
				var gotErrors []int32
				var nilResponse bool
				if method == "AddItems" {
					client, clientErr := itemmgt.NewItemManagementClient(
						ctx,
						conn,
						dcom.WithIPID(&dcom.IPID{}),
					)
					if clientErr != nil {
						t.Fatal(clientErr)
					}
					out, callErr := client.AddItems(ctx, &itemmgt.AddItemsRequest{Count: 2})
					err, nilResponse = callErr, out == nil
					if out != nil {
						status, gotErrors = out.Return, out.Errors
						if len(out.AddResults) != 2 || out.AddResults[0].Server != 42 ||
							out.AddResults[1].Server != 43 {
							t.Fatalf("lost item registrations: %+v", out)
						}
					}
				} else {
					client, clientErr := syncio.NewSyncIOClient(
						ctx,
						conn,
						dcom.WithIPID(&dcom.IPID{}),
					)
					if clientErr != nil {
						t.Fatal(clientErr)
					}
					if method == "Read" {
						out, callErr := client.Read(
							ctx,
							&syncio.ReadRequest{Count: 2, Server: []uint32{1, 2}},
						)
						err, nilResponse = callErr, out == nil
						if out != nil {
							status, gotErrors = out.Return, out.Errors
							if len(out.ItemValues) != 2 || out.ItemValues[0].Client != 42 ||
								out.ItemValues[0].Quality != 192 ||
								out.ItemValues[1].Client != 43 {
								t.Fatalf("lost item states: %+v", out)
							}
						}
					} else {
						out, callErr := client.Write(
							ctx,
							&syncio.WriteRequest{Count: 2, Server: []uint32{1, 2}},
						)
						err, nilResponse = callErr, out == nil
						if out != nil {
							status, gotErrors = out.Return, out.Errors
						}
					}
				}
				if (err != nil) != tc.wantErr {
					t.Fatalf("error=%v, wantErr=%t", err, tc.wantErr)
				}
				if tc.transport != nil {
					if !errors.Is(err, transportErr) || !nilResponse {
						t.Fatalf("transport failure: response nil=%t, error=%v", nilResponse, err)
					}
				} else if nilResponse || status != tc.status || !slices.Equal(gotErrors, itemErrors) {
					t.Fatalf(
						"lost response: nil=%t, status=%d, errors=%v",
						nilResponse,
						status,
						gotErrors,
					)
				}
			})
		}
	}
}
