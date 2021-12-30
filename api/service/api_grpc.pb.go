// Code generated by protoc-gen-go-grpc. DO NOT EDIT.
// versions:
// - protoc-gen-go-grpc v1.2.0
// - protoc             v3.6.1
// source: api.proto

package service

import (
	context "context"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
// Requires gRPC-Go v1.32.0 or later.
const _ = grpc.SupportPackageIsVersion7

// TrojanClientServiceClient is the client API for TrojanClientService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type TrojanClientServiceClient interface {
	GetTraffic(ctx context.Context, in *GetTrafficRequest, opts ...grpc.CallOption) (*GetTrafficResponse, error)
}

type trojanClientServiceClient struct {
	cc grpc.ClientConnInterface
}

func NewTrojanClientServiceClient(cc grpc.ClientConnInterface) TrojanClientServiceClient {
	return &trojanClientServiceClient{cc}
}

func (c *trojanClientServiceClient) GetTraffic(ctx context.Context, in *GetTrafficRequest, opts ...grpc.CallOption) (*GetTrafficResponse, error) {
	out := new(GetTrafficResponse)
	err := c.cc.Invoke(ctx, "/trojan.api.TrojanClientService/GetTraffic", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// TrojanClientServiceServer is the server API for TrojanClientService service.
// All implementations must embed UnimplementedTrojanClientServiceServer
// for forward compatibility
type TrojanClientServiceServer interface {
	GetTraffic(context.Context, *GetTrafficRequest) (*GetTrafficResponse, error)
	mustEmbedUnimplementedTrojanClientServiceServer()
}

// UnimplementedTrojanClientServiceServer must be embedded to have forward compatible implementations.
type UnimplementedTrojanClientServiceServer struct {
}

func (UnimplementedTrojanClientServiceServer) GetTraffic(context.Context, *GetTrafficRequest) (*GetTrafficResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetTraffic not implemented")
}
func (UnimplementedTrojanClientServiceServer) mustEmbedUnimplementedTrojanClientServiceServer() {}

// UnsafeTrojanClientServiceServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to TrojanClientServiceServer will
// result in compilation errors.
type UnsafeTrojanClientServiceServer interface {
	mustEmbedUnimplementedTrojanClientServiceServer()
}

func RegisterTrojanClientServiceServer(s grpc.ServiceRegistrar, srv TrojanClientServiceServer) {
	s.RegisterService(&TrojanClientService_ServiceDesc, srv)
}

func _TrojanClientService_GetTraffic_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetTrafficRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(TrojanClientServiceServer).GetTraffic(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/trojan.api.TrojanClientService/GetTraffic",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(TrojanClientServiceServer).GetTraffic(ctx, req.(*GetTrafficRequest))
	}
	return interceptor(ctx, in, info, handler)
}

// TrojanClientService_ServiceDesc is the grpc.ServiceDesc for TrojanClientService service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var TrojanClientService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "trojan.api.TrojanClientService",
	HandlerType: (*TrojanClientServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "GetTraffic",
			Handler:    _TrojanClientService_GetTraffic_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "api.proto",
}

// TrojanServerServiceClient is the client API for TrojanServerService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type TrojanServerServiceClient interface {
	// list all users
	ListUsers(ctx context.Context, in *ListUsersRequest, opts ...grpc.CallOption) (TrojanServerService_ListUsersClient, error)
	// obtain specified user's info
	GetUsers(ctx context.Context, opts ...grpc.CallOption) (TrojanServerService_GetUsersClient, error)
	// setup existing users' config
	SetUsers(ctx context.Context, opts ...grpc.CallOption) (TrojanServerService_SetUsersClient, error)
	// get traffic records
	GetRecords(ctx context.Context, in *GetRecordsRequest, opts ...grpc.CallOption) (TrojanServerService_GetRecordsClient, error)
}

type trojanServerServiceClient struct {
	cc grpc.ClientConnInterface
}

func NewTrojanServerServiceClient(cc grpc.ClientConnInterface) TrojanServerServiceClient {
	return &trojanServerServiceClient{cc}
}

func (c *trojanServerServiceClient) ListUsers(ctx context.Context, in *ListUsersRequest, opts ...grpc.CallOption) (TrojanServerService_ListUsersClient, error) {
	stream, err := c.cc.NewStream(ctx, &TrojanServerService_ServiceDesc.Streams[0], "/trojan.api.TrojanServerService/ListUsers", opts...)
	if err != nil {
		return nil, err
	}
	x := &trojanServerServiceListUsersClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type TrojanServerService_ListUsersClient interface {
	Recv() (*ListUsersResponse, error)
	grpc.ClientStream
}

type trojanServerServiceListUsersClient struct {
	grpc.ClientStream
}

func (x *trojanServerServiceListUsersClient) Recv() (*ListUsersResponse, error) {
	m := new(ListUsersResponse)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *trojanServerServiceClient) GetUsers(ctx context.Context, opts ...grpc.CallOption) (TrojanServerService_GetUsersClient, error) {
	stream, err := c.cc.NewStream(ctx, &TrojanServerService_ServiceDesc.Streams[1], "/trojan.api.TrojanServerService/GetUsers", opts...)
	if err != nil {
		return nil, err
	}
	x := &trojanServerServiceGetUsersClient{stream}
	return x, nil
}

type TrojanServerService_GetUsersClient interface {
	Send(*GetUsersRequest) error
	Recv() (*GetUsersResponse, error)
	grpc.ClientStream
}

type trojanServerServiceGetUsersClient struct {
	grpc.ClientStream
}

func (x *trojanServerServiceGetUsersClient) Send(m *GetUsersRequest) error {
	return x.ClientStream.SendMsg(m)
}

func (x *trojanServerServiceGetUsersClient) Recv() (*GetUsersResponse, error) {
	m := new(GetUsersResponse)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *trojanServerServiceClient) SetUsers(ctx context.Context, opts ...grpc.CallOption) (TrojanServerService_SetUsersClient, error) {
	stream, err := c.cc.NewStream(ctx, &TrojanServerService_ServiceDesc.Streams[2], "/trojan.api.TrojanServerService/SetUsers", opts...)
	if err != nil {
		return nil, err
	}
	x := &trojanServerServiceSetUsersClient{stream}
	return x, nil
}

type TrojanServerService_SetUsersClient interface {
	Send(*SetUsersRequest) error
	Recv() (*SetUsersResponse, error)
	grpc.ClientStream
}

type trojanServerServiceSetUsersClient struct {
	grpc.ClientStream
}

func (x *trojanServerServiceSetUsersClient) Send(m *SetUsersRequest) error {
	return x.ClientStream.SendMsg(m)
}

func (x *trojanServerServiceSetUsersClient) Recv() (*SetUsersResponse, error) {
	m := new(SetUsersResponse)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *trojanServerServiceClient) GetRecords(ctx context.Context, in *GetRecordsRequest, opts ...grpc.CallOption) (TrojanServerService_GetRecordsClient, error) {
	stream, err := c.cc.NewStream(ctx, &TrojanServerService_ServiceDesc.Streams[3], "/trojan.api.TrojanServerService/GetRecords", opts...)
	if err != nil {
		return nil, err
	}
	x := &trojanServerServiceGetRecordsClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type TrojanServerService_GetRecordsClient interface {
	Recv() (*GetRecordsResponse, error)
	grpc.ClientStream
}

type trojanServerServiceGetRecordsClient struct {
	grpc.ClientStream
}

func (x *trojanServerServiceGetRecordsClient) Recv() (*GetRecordsResponse, error) {
	m := new(GetRecordsResponse)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

// TrojanServerServiceServer is the server API for TrojanServerService service.
// All implementations must embed UnimplementedTrojanServerServiceServer
// for forward compatibility
type TrojanServerServiceServer interface {
	// list all users
	ListUsers(*ListUsersRequest, TrojanServerService_ListUsersServer) error
	// obtain specified user's info
	GetUsers(TrojanServerService_GetUsersServer) error
	// setup existing users' config
	SetUsers(TrojanServerService_SetUsersServer) error
	// get traffic records
	GetRecords(*GetRecordsRequest, TrojanServerService_GetRecordsServer) error
	mustEmbedUnimplementedTrojanServerServiceServer()
}

// UnimplementedTrojanServerServiceServer must be embedded to have forward compatible implementations.
type UnimplementedTrojanServerServiceServer struct {
}

func (UnimplementedTrojanServerServiceServer) ListUsers(*ListUsersRequest, TrojanServerService_ListUsersServer) error {
	return status.Errorf(codes.Unimplemented, "method ListUsers not implemented")
}
func (UnimplementedTrojanServerServiceServer) GetUsers(TrojanServerService_GetUsersServer) error {
	return status.Errorf(codes.Unimplemented, "method GetUsers not implemented")
}
func (UnimplementedTrojanServerServiceServer) SetUsers(TrojanServerService_SetUsersServer) error {
	return status.Errorf(codes.Unimplemented, "method SetUsers not implemented")
}
func (UnimplementedTrojanServerServiceServer) GetRecords(*GetRecordsRequest, TrojanServerService_GetRecordsServer) error {
	return status.Errorf(codes.Unimplemented, "method GetRecords not implemented")
}
func (UnimplementedTrojanServerServiceServer) mustEmbedUnimplementedTrojanServerServiceServer() {}

// UnsafeTrojanServerServiceServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to TrojanServerServiceServer will
// result in compilation errors.
type UnsafeTrojanServerServiceServer interface {
	mustEmbedUnimplementedTrojanServerServiceServer()
}

func RegisterTrojanServerServiceServer(s grpc.ServiceRegistrar, srv TrojanServerServiceServer) {
	s.RegisterService(&TrojanServerService_ServiceDesc, srv)
}

func _TrojanServerService_ListUsers_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(ListUsersRequest)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(TrojanServerServiceServer).ListUsers(m, &trojanServerServiceListUsersServer{stream})
}

type TrojanServerService_ListUsersServer interface {
	Send(*ListUsersResponse) error
	grpc.ServerStream
}

type trojanServerServiceListUsersServer struct {
	grpc.ServerStream
}

func (x *trojanServerServiceListUsersServer) Send(m *ListUsersResponse) error {
	return x.ServerStream.SendMsg(m)
}

func _TrojanServerService_GetUsers_Handler(srv interface{}, stream grpc.ServerStream) error {
	return srv.(TrojanServerServiceServer).GetUsers(&trojanServerServiceGetUsersServer{stream})
}

type TrojanServerService_GetUsersServer interface {
	Send(*GetUsersResponse) error
	Recv() (*GetUsersRequest, error)
	grpc.ServerStream
}

type trojanServerServiceGetUsersServer struct {
	grpc.ServerStream
}

func (x *trojanServerServiceGetUsersServer) Send(m *GetUsersResponse) error {
	return x.ServerStream.SendMsg(m)
}

func (x *trojanServerServiceGetUsersServer) Recv() (*GetUsersRequest, error) {
	m := new(GetUsersRequest)
	if err := x.ServerStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func _TrojanServerService_SetUsers_Handler(srv interface{}, stream grpc.ServerStream) error {
	return srv.(TrojanServerServiceServer).SetUsers(&trojanServerServiceSetUsersServer{stream})
}

type TrojanServerService_SetUsersServer interface {
	Send(*SetUsersResponse) error
	Recv() (*SetUsersRequest, error)
	grpc.ServerStream
}

type trojanServerServiceSetUsersServer struct {
	grpc.ServerStream
}

func (x *trojanServerServiceSetUsersServer) Send(m *SetUsersResponse) error {
	return x.ServerStream.SendMsg(m)
}

func (x *trojanServerServiceSetUsersServer) Recv() (*SetUsersRequest, error) {
	m := new(SetUsersRequest)
	if err := x.ServerStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func _TrojanServerService_GetRecords_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(GetRecordsRequest)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(TrojanServerServiceServer).GetRecords(m, &trojanServerServiceGetRecordsServer{stream})
}

type TrojanServerService_GetRecordsServer interface {
	Send(*GetRecordsResponse) error
	grpc.ServerStream
}

type trojanServerServiceGetRecordsServer struct {
	grpc.ServerStream
}

func (x *trojanServerServiceGetRecordsServer) Send(m *GetRecordsResponse) error {
	return x.ServerStream.SendMsg(m)
}

// TrojanServerService_ServiceDesc is the grpc.ServiceDesc for TrojanServerService service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var TrojanServerService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "trojan.api.TrojanServerService",
	HandlerType: (*TrojanServerServiceServer)(nil),
	Methods:     []grpc.MethodDesc{},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "ListUsers",
			Handler:       _TrojanServerService_ListUsers_Handler,
			ServerStreams: true,
		},
		{
			StreamName:    "GetUsers",
			Handler:       _TrojanServerService_GetUsers_Handler,
			ServerStreams: true,
			ClientStreams: true,
		},
		{
			StreamName:    "SetUsers",
			Handler:       _TrojanServerService_SetUsers_Handler,
			ServerStreams: true,
			ClientStreams: true,
		},
		{
			StreamName:    "GetRecords",
			Handler:       _TrojanServerService_GetRecords_Handler,
			ServerStreams: true,
		},
	},
	Metadata: "api.proto",
}
