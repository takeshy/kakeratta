package store

import (
	"context"
	"net"
	"strings"
	"testing"

	"cloud.google.com/go/firestore"
	pb "cloud.google.com/go/firestore/apiv1/firestorepb"
	"github.com/takeshy/kakeratta/internal/timeline"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type vectorRPC struct {
	pb.UnimplementedFirestoreServer
	queries chan *pb.RunQueryRequest
	commits chan *pb.CommitRequest
}

func stringValue(value string) *pb.Value {
	return &pb.Value{ValueType: &pb.Value_StringValue{StringValue: value}}
}

func (s *vectorRPC) RunQuery(req *pb.RunQueryRequest, stream pb.Firestore_RunQueryServer) error {
	s.queries <- req
	return stream.Send(&pb.RunQueryResponse{Document: &pb.Document{
		Name:       req.Parent + "/searchPassages/passage",
		Fields:     map[string]*pb.Value{"entryId": stringValue("p"), "hash": stringValue("hash"), "text": stringValue("ワイン"), "distance": {ValueType: &pb.Value_DoubleValue{DoubleValue: 0.25}}},
		CreateTime: timestamppb.Now(), UpdateTime: timestamppb.Now(),
	}, ReadTime: timestamppb.Now()})
}

func (s *vectorRPC) Commit(_ context.Context, req *pb.CommitRequest) (*pb.CommitResponse, error) {
	s.commits <- req
	results := make([]*pb.WriteResult, len(req.Writes))
	for i := range results {
		results[i] = &pb.WriteResult{UpdateTime: timestamppb.Now()}
	}
	return &pb.CommitResponse{WriteResults: results, CommitTime: timestamppb.Now()}, nil
}

func TestFirestoreUsesNativeVectorWireFormatAndScopedFindNearest(t *testing.T) {
	ctx := context.Background()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	rpc := &vectorRPC{queries: make(chan *pb.RunQueryRequest, 1), commits: make(chan *pb.CommitRequest, 2)}
	pb.RegisterFirestoreServer(server, rpc)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	connection, err := grpc.DialContext(ctx, "bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	client, err := firestore.NewClient(ctx, "test-project", option.WithGRPCConn(connection))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	w := &FirestoreWorkspace{client: client, user: client.Collection("users").Doc("alice")}
	vector := make([]float64, 1024)
	vector[0] = 1
	hits, err := w.NearestSearchPassages(ctx, "model-key", vector, 20)
	if err != nil || len(hits) != 1 || hits[0].Distance != 0.25 {
		t.Fatalf("%+v %v", hits, err)
	}
	req := <-rpc.queries
	query := req.GetStructuredQuery()
	if !strings.HasSuffix(req.Parent, "/users/alice") || query.From[0].CollectionId != "searchPassages" || query.From[0].AllDescendants {
		t.Fatalf("unscoped query: %v", req)
	}
	nearest := query.FindNearest
	if nearest == nil || nearest.VectorField.FieldPath != "embedding" || nearest.DistanceMeasure != pb.StructuredQuery_FindNearest_COSINE || nearest.Limit.Value != 20 {
		t.Fatalf("not a native cosine query: %v", query)
	}
	filter := query.Where.GetFieldFilter()
	if filter.Field.FieldPath != "modelKey" || filter.Value.GetStringValue() != "model-key" {
		t.Fatalf("missing model isolation: %v", filter)
	}
	if _, err := w.searchPassages().Doc("p").Set(ctx, SearchPassage{EntryID: "p", Embedding: firestore.Vector64(vector)}); err != nil {
		t.Fatal(err)
	}
	write := (<-rpc.commits).Writes[0].GetUpdate()
	encoded := write.Fields["embedding"].GetMapValue().Fields
	if encoded["__type__"].GetStringValue() != "__vector__" || len(encoded["value"].GetArrayValue().Values) != 1024 {
		t.Fatal("embedding is not stored as a native 1024-dimensional vector")
	}
	if err := w.Append(timeline.Entry{ID: "p", Kind: timeline.KindPost, Text: "new"}); err != nil {
		t.Fatal(err)
	}
	write = (<-rpc.commits).Writes[0].GetUpdate()
	if !write.Fields["SearchDirty"].GetBooleanValue() {
		t.Fatal("post mutation did not schedule reindexing")
	}
}
