// Code generated by protoc-gen-go-grpc. DO NOT EDIT.

package proto

import (
	context "context"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
const _ = grpc.SupportPackageIsVersion7

// BandcampServerServiceClient is the client API for BandcampServerService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type BandcampServerServiceClient interface {
	SetToken(ctx context.Context, in *SetTokenRequest, opts ...grpc.CallOption) (*SetTokenResponse, error)
	AddMapping(ctx context.Context, in *AddMappingRequest, opts ...grpc.CallOption) (*AddMappingResponse, error)
}

type bandcampServerServiceClient struct {
	cc grpc.ClientConnInterface
}

func NewBandcampServerServiceClient(cc grpc.ClientConnInterface) BandcampServerServiceClient {
	return &bandcampServerServiceClient{cc}
}

func (c *bandcampServerServiceClient) SetToken(ctx context.Context, in *SetTokenRequest, opts ...grpc.CallOption) (*SetTokenResponse, error) {
	out := new(SetTokenResponse)
	err := c.cc.Invoke(ctx, "/bandcampserver.BandcampServerService/SetToken", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *bandcampServerServiceClient) AddMapping(ctx context.Context, in *AddMappingRequest, opts ...grpc.CallOption) (*AddMappingResponse, error) {
	out := new(AddMappingResponse)
	err := c.cc.Invoke(ctx, "/bandcampserver.BandcampServerService/AddMapping", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// BandcampServerServiceServer is the server API for BandcampServerService service.
// All implementations should embed UnimplementedBandcampServerServiceServer
// for forward compatibility
type BandcampServerServiceServer interface {
	SetToken(context.Context, *SetTokenRequest) (*SetTokenResponse, error)
	AddMapping(context.Context, *AddMappingRequest) (*AddMappingResponse, error)
}

// UnimplementedBandcampServerServiceServer should be embedded to have forward compatible implementations.
type UnimplementedBandcampServerServiceServer struct {
}

func (UnimplementedBandcampServerServiceServer) SetToken(context.Context, *SetTokenRequest) (*SetTokenResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method SetToken not implemented")
}
func (UnimplementedBandcampServerServiceServer) AddMapping(context.Context, *AddMappingRequest) (*AddMappingResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method AddMapping not implemented")
}

// UnsafeBandcampServerServiceServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to BandcampServerServiceServer will
// result in compilation errors.
type UnsafeBandcampServerServiceServer interface {
	mustEmbedUnimplementedBandcampServerServiceServer()
}

func RegisterBandcampServerServiceServer(s grpc.ServiceRegistrar, srv BandcampServerServiceServer) {
	s.RegisterService(&_BandcampServerService_serviceDesc, srv)
}

func _BandcampServerService_SetToken_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(SetTokenRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(BandcampServerServiceServer).SetToken(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/bandcampserver.BandcampServerService/SetToken",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(BandcampServerServiceServer).SetToken(ctx, req.(*SetTokenRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _BandcampServerService_AddMapping_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(AddMappingRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(BandcampServerServiceServer).AddMapping(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/bandcampserver.BandcampServerService/AddMapping",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(BandcampServerServiceServer).AddMapping(ctx, req.(*AddMappingRequest))
	}
	return interceptor(ctx, in, info, handler)
}

var _BandcampServerService_serviceDesc = grpc.ServiceDesc{
	ServiceName: "bandcampserver.BandcampServerService",
	HandlerType: (*BandcampServerServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "SetToken",
			Handler:    _BandcampServerService_SetToken_Handler,
		},
		{
			MethodName: "AddMapping",
			Handler:    _BandcampServerService_AddMapping_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "bandcampserver.proto",
}
