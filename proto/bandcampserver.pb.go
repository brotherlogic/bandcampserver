// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.25.0
// 	protoc        v3.6.1
// source: bandcampserver.proto

package proto

import (
	proto1 "github.com/brotherlogic/bandcamplib/proto"
	proto "github.com/golang/protobuf/proto"
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	reflect "reflect"
	sync "sync"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

// This is a compile-time assertion that a sufficiently up-to-date version
// of the legacy proto package is being used.
const _ = proto.ProtoPackageIsVersion4

type Config struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Token            string          `protobuf:"bytes,1,opt,name=token,proto3" json:"token,omitempty"`
	LastTokenRefresh int64           `protobuf:"varint,2,opt,name=last_token_refresh,json=lastTokenRefresh,proto3" json:"last_token_refresh,omitempty"`
	Items            []*proto1.Item  `protobuf:"bytes,3,rep,name=items,proto3" json:"items,omitempty"`
	Mapping          map[int64]int32 `protobuf:"bytes,4,rep,name=mapping,proto3" json:"mapping,omitempty" protobuf_key:"varint,1,opt,name=key,proto3" protobuf_val:"varint,2,opt,name=value,proto3"`
	IssueIds         map[int64]int32 `protobuf:"bytes,5,rep,name=issue_ids,json=issueIds,proto3" json:"issue_ids,omitempty" protobuf_key:"varint,1,opt,name=key,proto3" protobuf_val:"varint,2,opt,name=value,proto3"`
}

func (x *Config) Reset() {
	*x = Config{}
	if protoimpl.UnsafeEnabled {
		mi := &file_bandcampserver_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Config) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Config) ProtoMessage() {}

func (x *Config) ProtoReflect() protoreflect.Message {
	mi := &file_bandcampserver_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Config.ProtoReflect.Descriptor instead.
func (*Config) Descriptor() ([]byte, []int) {
	return file_bandcampserver_proto_rawDescGZIP(), []int{0}
}

func (x *Config) GetToken() string {
	if x != nil {
		return x.Token
	}
	return ""
}

func (x *Config) GetLastTokenRefresh() int64 {
	if x != nil {
		return x.LastTokenRefresh
	}
	return 0
}

func (x *Config) GetItems() []*proto1.Item {
	if x != nil {
		return x.Items
	}
	return nil
}

func (x *Config) GetMapping() map[int64]int32 {
	if x != nil {
		return x.Mapping
	}
	return nil
}

func (x *Config) GetIssueIds() map[int64]int32 {
	if x != nil {
		return x.IssueIds
	}
	return nil
}

type SetTokenRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Token string `protobuf:"bytes,1,opt,name=token,proto3" json:"token,omitempty"`
}

func (x *SetTokenRequest) Reset() {
	*x = SetTokenRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_bandcampserver_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *SetTokenRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*SetTokenRequest) ProtoMessage() {}

func (x *SetTokenRequest) ProtoReflect() protoreflect.Message {
	mi := &file_bandcampserver_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use SetTokenRequest.ProtoReflect.Descriptor instead.
func (*SetTokenRequest) Descriptor() ([]byte, []int) {
	return file_bandcampserver_proto_rawDescGZIP(), []int{1}
}

func (x *SetTokenRequest) GetToken() string {
	if x != nil {
		return x.Token
	}
	return ""
}

type SetTokenResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields
}

func (x *SetTokenResponse) Reset() {
	*x = SetTokenResponse{}
	if protoimpl.UnsafeEnabled {
		mi := &file_bandcampserver_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *SetTokenResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*SetTokenResponse) ProtoMessage() {}

func (x *SetTokenResponse) ProtoReflect() protoreflect.Message {
	mi := &file_bandcampserver_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use SetTokenResponse.ProtoReflect.Descriptor instead.
func (*SetTokenResponse) Descriptor() ([]byte, []int) {
	return file_bandcampserver_proto_rawDescGZIP(), []int{2}
}

type AddMappingRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	BandcampId int64 `protobuf:"varint,1,opt,name=bandcamp_id,json=bandcampId,proto3" json:"bandcamp_id,omitempty"`
	DiscogsId  int32 `protobuf:"varint,2,opt,name=discogs_id,json=discogsId,proto3" json:"discogs_id,omitempty"`
}

func (x *AddMappingRequest) Reset() {
	*x = AddMappingRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_bandcampserver_proto_msgTypes[3]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *AddMappingRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*AddMappingRequest) ProtoMessage() {}

func (x *AddMappingRequest) ProtoReflect() protoreflect.Message {
	mi := &file_bandcampserver_proto_msgTypes[3]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use AddMappingRequest.ProtoReflect.Descriptor instead.
func (*AddMappingRequest) Descriptor() ([]byte, []int) {
	return file_bandcampserver_proto_rawDescGZIP(), []int{3}
}

func (x *AddMappingRequest) GetBandcampId() int64 {
	if x != nil {
		return x.BandcampId
	}
	return 0
}

func (x *AddMappingRequest) GetDiscogsId() int32 {
	if x != nil {
		return x.DiscogsId
	}
	return 0
}

type AddMappingResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields
}

func (x *AddMappingResponse) Reset() {
	*x = AddMappingResponse{}
	if protoimpl.UnsafeEnabled {
		mi := &file_bandcampserver_proto_msgTypes[4]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *AddMappingResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*AddMappingResponse) ProtoMessage() {}

func (x *AddMappingResponse) ProtoReflect() protoreflect.Message {
	mi := &file_bandcampserver_proto_msgTypes[4]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use AddMappingResponse.ProtoReflect.Descriptor instead.
func (*AddMappingResponse) Descriptor() ([]byte, []int) {
	return file_bandcampserver_proto_rawDescGZIP(), []int{4}
}

var File_bandcampserver_proto protoreflect.FileDescriptor

var file_bandcampserver_proto_rawDesc = []byte{
	0x0a, 0x14, 0x62, 0x61, 0x6e, 0x64, 0x63, 0x61, 0x6d, 0x70, 0x73, 0x65, 0x72, 0x76, 0x65, 0x72,
	0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x0e, 0x62, 0x61, 0x6e, 0x64, 0x63, 0x61, 0x6d, 0x70,
	0x73, 0x65, 0x72, 0x76, 0x65, 0x72, 0x1a, 0x38, 0x67, 0x69, 0x74, 0x68, 0x75, 0x62, 0x2e, 0x63,
	0x6f, 0x6d, 0x2f, 0x62, 0x72, 0x6f, 0x74, 0x68, 0x65, 0x72, 0x6c, 0x6f, 0x67, 0x69, 0x63, 0x2f,
	0x62, 0x61, 0x6e, 0x64, 0x63, 0x61, 0x6d, 0x70, 0x6c, 0x69, 0x62, 0x2f, 0x70, 0x72, 0x6f, 0x74,
	0x6f, 0x2f, 0x62, 0x61, 0x6e, 0x64, 0x63, 0x61, 0x6d, 0x70, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f,
	0x22, 0xf0, 0x02, 0x0a, 0x06, 0x43, 0x6f, 0x6e, 0x66, 0x69, 0x67, 0x12, 0x14, 0x0a, 0x05, 0x74,
	0x6f, 0x6b, 0x65, 0x6e, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x05, 0x74, 0x6f, 0x6b, 0x65,
	0x6e, 0x12, 0x2c, 0x0a, 0x12, 0x6c, 0x61, 0x73, 0x74, 0x5f, 0x74, 0x6f, 0x6b, 0x65, 0x6e, 0x5f,
	0x72, 0x65, 0x66, 0x72, 0x65, 0x73, 0x68, 0x18, 0x02, 0x20, 0x01, 0x28, 0x03, 0x52, 0x10, 0x6c,
	0x61, 0x73, 0x74, 0x54, 0x6f, 0x6b, 0x65, 0x6e, 0x52, 0x65, 0x66, 0x72, 0x65, 0x73, 0x68, 0x12,
	0x27, 0x0a, 0x05, 0x69, 0x74, 0x65, 0x6d, 0x73, 0x18, 0x03, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x11,
	0x2e, 0x62, 0x61, 0x6e, 0x64, 0x63, 0x61, 0x6d, 0x70, 0x6c, 0x69, 0x62, 0x2e, 0x49, 0x74, 0x65,
	0x6d, 0x52, 0x05, 0x69, 0x74, 0x65, 0x6d, 0x73, 0x12, 0x3d, 0x0a, 0x07, 0x6d, 0x61, 0x70, 0x70,
	0x69, 0x6e, 0x67, 0x18, 0x04, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x23, 0x2e, 0x62, 0x61, 0x6e, 0x64,
	0x63, 0x61, 0x6d, 0x70, 0x73, 0x65, 0x72, 0x76, 0x65, 0x72, 0x2e, 0x43, 0x6f, 0x6e, 0x66, 0x69,
	0x67, 0x2e, 0x4d, 0x61, 0x70, 0x70, 0x69, 0x6e, 0x67, 0x45, 0x6e, 0x74, 0x72, 0x79, 0x52, 0x07,
	0x6d, 0x61, 0x70, 0x70, 0x69, 0x6e, 0x67, 0x12, 0x41, 0x0a, 0x09, 0x69, 0x73, 0x73, 0x75, 0x65,
	0x5f, 0x69, 0x64, 0x73, 0x18, 0x05, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x24, 0x2e, 0x62, 0x61, 0x6e,
	0x64, 0x63, 0x61, 0x6d, 0x70, 0x73, 0x65, 0x72, 0x76, 0x65, 0x72, 0x2e, 0x43, 0x6f, 0x6e, 0x66,
	0x69, 0x67, 0x2e, 0x49, 0x73, 0x73, 0x75, 0x65, 0x49, 0x64, 0x73, 0x45, 0x6e, 0x74, 0x72, 0x79,
	0x52, 0x08, 0x69, 0x73, 0x73, 0x75, 0x65, 0x49, 0x64, 0x73, 0x1a, 0x3a, 0x0a, 0x0c, 0x4d, 0x61,
	0x70, 0x70, 0x69, 0x6e, 0x67, 0x45, 0x6e, 0x74, 0x72, 0x79, 0x12, 0x10, 0x0a, 0x03, 0x6b, 0x65,
	0x79, 0x18, 0x01, 0x20, 0x01, 0x28, 0x03, 0x52, 0x03, 0x6b, 0x65, 0x79, 0x12, 0x14, 0x0a, 0x05,
	0x76, 0x61, 0x6c, 0x75, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x05, 0x52, 0x05, 0x76, 0x61, 0x6c,
	0x75, 0x65, 0x3a, 0x02, 0x38, 0x01, 0x1a, 0x3b, 0x0a, 0x0d, 0x49, 0x73, 0x73, 0x75, 0x65, 0x49,
	0x64, 0x73, 0x45, 0x6e, 0x74, 0x72, 0x79, 0x12, 0x10, 0x0a, 0x03, 0x6b, 0x65, 0x79, 0x18, 0x01,
	0x20, 0x01, 0x28, 0x03, 0x52, 0x03, 0x6b, 0x65, 0x79, 0x12, 0x14, 0x0a, 0x05, 0x76, 0x61, 0x6c,
	0x75, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x05, 0x52, 0x05, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x3a,
	0x02, 0x38, 0x01, 0x22, 0x27, 0x0a, 0x0f, 0x53, 0x65, 0x74, 0x54, 0x6f, 0x6b, 0x65, 0x6e, 0x52,
	0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x14, 0x0a, 0x05, 0x74, 0x6f, 0x6b, 0x65, 0x6e, 0x18,
	0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x05, 0x74, 0x6f, 0x6b, 0x65, 0x6e, 0x22, 0x12, 0x0a, 0x10,
	0x53, 0x65, 0x74, 0x54, 0x6f, 0x6b, 0x65, 0x6e, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65,
	0x22, 0x53, 0x0a, 0x11, 0x41, 0x64, 0x64, 0x4d, 0x61, 0x70, 0x70, 0x69, 0x6e, 0x67, 0x52, 0x65,
	0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x1f, 0x0a, 0x0b, 0x62, 0x61, 0x6e, 0x64, 0x63, 0x61, 0x6d,
	0x70, 0x5f, 0x69, 0x64, 0x18, 0x01, 0x20, 0x01, 0x28, 0x03, 0x52, 0x0a, 0x62, 0x61, 0x6e, 0x64,
	0x63, 0x61, 0x6d, 0x70, 0x49, 0x64, 0x12, 0x1d, 0x0a, 0x0a, 0x64, 0x69, 0x73, 0x63, 0x6f, 0x67,
	0x73, 0x5f, 0x69, 0x64, 0x18, 0x02, 0x20, 0x01, 0x28, 0x05, 0x52, 0x09, 0x64, 0x69, 0x73, 0x63,
	0x6f, 0x67, 0x73, 0x49, 0x64, 0x22, 0x14, 0x0a, 0x12, 0x41, 0x64, 0x64, 0x4d, 0x61, 0x70, 0x70,
	0x69, 0x6e, 0x67, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x32, 0xbf, 0x01, 0x0a, 0x15,
	0x42, 0x61, 0x6e, 0x64, 0x63, 0x61, 0x6d, 0x70, 0x53, 0x65, 0x72, 0x76, 0x65, 0x72, 0x53, 0x65,
	0x72, 0x76, 0x69, 0x63, 0x65, 0x12, 0x4f, 0x0a, 0x08, 0x53, 0x65, 0x74, 0x54, 0x6f, 0x6b, 0x65,
	0x6e, 0x12, 0x1f, 0x2e, 0x62, 0x61, 0x6e, 0x64, 0x63, 0x61, 0x6d, 0x70, 0x73, 0x65, 0x72, 0x76,
	0x65, 0x72, 0x2e, 0x53, 0x65, 0x74, 0x54, 0x6f, 0x6b, 0x65, 0x6e, 0x52, 0x65, 0x71, 0x75, 0x65,
	0x73, 0x74, 0x1a, 0x20, 0x2e, 0x62, 0x61, 0x6e, 0x64, 0x63, 0x61, 0x6d, 0x70, 0x73, 0x65, 0x72,
	0x76, 0x65, 0x72, 0x2e, 0x53, 0x65, 0x74, 0x54, 0x6f, 0x6b, 0x65, 0x6e, 0x52, 0x65, 0x73, 0x70,
	0x6f, 0x6e, 0x73, 0x65, 0x22, 0x00, 0x12, 0x55, 0x0a, 0x0a, 0x41, 0x64, 0x64, 0x4d, 0x61, 0x70,
	0x70, 0x69, 0x6e, 0x67, 0x12, 0x21, 0x2e, 0x62, 0x61, 0x6e, 0x64, 0x63, 0x61, 0x6d, 0x70, 0x73,
	0x65, 0x72, 0x76, 0x65, 0x72, 0x2e, 0x41, 0x64, 0x64, 0x4d, 0x61, 0x70, 0x70, 0x69, 0x6e, 0x67,
	0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x22, 0x2e, 0x62, 0x61, 0x6e, 0x64, 0x63, 0x61,
	0x6d, 0x70, 0x73, 0x65, 0x72, 0x76, 0x65, 0x72, 0x2e, 0x41, 0x64, 0x64, 0x4d, 0x61, 0x70, 0x70,
	0x69, 0x6e, 0x67, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22, 0x00, 0x42, 0x2e, 0x5a,
	0x2c, 0x67, 0x69, 0x74, 0x68, 0x75, 0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x62, 0x72, 0x6f, 0x74,
	0x68, 0x65, 0x72, 0x6c, 0x6f, 0x67, 0x69, 0x63, 0x2f, 0x62, 0x61, 0x6e, 0x64, 0x63, 0x61, 0x6d,
	0x70, 0x73, 0x65, 0x72, 0x76, 0x65, 0x72, 0x2f, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x06, 0x70,
	0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_bandcampserver_proto_rawDescOnce sync.Once
	file_bandcampserver_proto_rawDescData = file_bandcampserver_proto_rawDesc
)

func file_bandcampserver_proto_rawDescGZIP() []byte {
	file_bandcampserver_proto_rawDescOnce.Do(func() {
		file_bandcampserver_proto_rawDescData = protoimpl.X.CompressGZIP(file_bandcampserver_proto_rawDescData)
	})
	return file_bandcampserver_proto_rawDescData
}

var file_bandcampserver_proto_msgTypes = make([]protoimpl.MessageInfo, 7)
var file_bandcampserver_proto_goTypes = []interface{}{
	(*Config)(nil),             // 0: bandcampserver.Config
	(*SetTokenRequest)(nil),    // 1: bandcampserver.SetTokenRequest
	(*SetTokenResponse)(nil),   // 2: bandcampserver.SetTokenResponse
	(*AddMappingRequest)(nil),  // 3: bandcampserver.AddMappingRequest
	(*AddMappingResponse)(nil), // 4: bandcampserver.AddMappingResponse
	nil,                        // 5: bandcampserver.Config.MappingEntry
	nil,                        // 6: bandcampserver.Config.IssueIdsEntry
	(*proto1.Item)(nil),        // 7: bandcamplib.Item
}
var file_bandcampserver_proto_depIdxs = []int32{
	7, // 0: bandcampserver.Config.items:type_name -> bandcamplib.Item
	5, // 1: bandcampserver.Config.mapping:type_name -> bandcampserver.Config.MappingEntry
	6, // 2: bandcampserver.Config.issue_ids:type_name -> bandcampserver.Config.IssueIdsEntry
	1, // 3: bandcampserver.BandcampServerService.SetToken:input_type -> bandcampserver.SetTokenRequest
	3, // 4: bandcampserver.BandcampServerService.AddMapping:input_type -> bandcampserver.AddMappingRequest
	2, // 5: bandcampserver.BandcampServerService.SetToken:output_type -> bandcampserver.SetTokenResponse
	4, // 6: bandcampserver.BandcampServerService.AddMapping:output_type -> bandcampserver.AddMappingResponse
	5, // [5:7] is the sub-list for method output_type
	3, // [3:5] is the sub-list for method input_type
	3, // [3:3] is the sub-list for extension type_name
	3, // [3:3] is the sub-list for extension extendee
	0, // [0:3] is the sub-list for field type_name
}

func init() { file_bandcampserver_proto_init() }
func file_bandcampserver_proto_init() {
	if File_bandcampserver_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_bandcampserver_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Config); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_bandcampserver_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*SetTokenRequest); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_bandcampserver_proto_msgTypes[2].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*SetTokenResponse); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_bandcampserver_proto_msgTypes[3].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*AddMappingRequest); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_bandcampserver_proto_msgTypes[4].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*AddMappingResponse); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_bandcampserver_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   7,
			NumExtensions: 0,
			NumServices:   1,
		},
		GoTypes:           file_bandcampserver_proto_goTypes,
		DependencyIndexes: file_bandcampserver_proto_depIdxs,
		MessageInfos:      file_bandcampserver_proto_msgTypes,
	}.Build()
	File_bandcampserver_proto = out.File
	file_bandcampserver_proto_rawDesc = nil
	file_bandcampserver_proto_goTypes = nil
	file_bandcampserver_proto_depIdxs = nil
}
