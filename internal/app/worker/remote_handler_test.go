package worker

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	computehopv1 "github.com/austinjiann/spare-compute/gen/go/computehop/v1"
	"github.com/austinjiann/spare-compute/internal/artifact"
	"github.com/austinjiann/spare-compute/internal/job"
	joblogging "github.com/austinjiann/spare-compute/internal/logging"
	"github.com/austinjiann/spare-compute/internal/protocol/mapper"
	"github.com/austinjiann/spare-compute/internal/snapshot"
	"github.com/austinjiann/spare-compute/internal/transfer"
)

func TestRemoteHandlerSubmitsValidatedJob(t *testing.T) {
	want := remoteHandlerJob(t)
	controller := remoteControllerStub{submit: func(_ context.Context, spec job.Spec) (job.Job, error) {
		if spec.Executable != "echo" || len(spec.Arguments) != 1 || spec.Arguments[0] != "hello" ||
			spec.WorkingDirectory != "/worker/project" {
			t.Fatalf("spec = %#v", spec)
		}
		return want, nil
	}}
	handler, err := NewRemoteHandler(controller)
	if err != nil {
		t.Fatal(err)
	}
	response := handler.Handle(context.Background(), &computehopv1.RemoteRequest{
		Operation: &computehopv1.RemoteRequest_SubmitJob{SubmitJob: &computehopv1.SubmitJobRequest{
			Spec: &computehopv1.JobSpec{
				Executable: "echo", Arguments: []string{"hello"}, WorkingDirectory: "/worker/project",
				Executor: computehopv1.Executor_EXECUTOR_NATIVE,
			},
		}},
	})
	if response.GetError() != nil || response.GetSubmitJob().GetJob().GetId() != string(want.ID) {
		t.Fatalf("response = %#v", response)
	}
}

func TestRemoteHandlerReturnsWorkerStatus(t *testing.T) {
	handler, err := NewRemoteHandler(remoteControllerStub{}, WithStatus(Status{
		Platform: "linux", Architecture: "amd64",
		LogicalCPUCount: 32, TotalMemoryBytes: 64 << 30,
		ToolIDs: []string{"docker", "go"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	response := handler.Handle(context.Background(), &computehopv1.RemoteRequest{
		Operation: &computehopv1.RemoteRequest_GetWorkerStatus{
			GetWorkerStatus: &computehopv1.GetWorkerStatusRequest{},
		},
	})
	status := response.GetGetWorkerStatus()
	if response.GetError() != nil || status.GetPlatform() != "linux" ||
		status.GetArch() != "amd64" || status.GetLogicalCpuCount() != 32 ||
		status.GetTotalMemoryBytes() != 64<<30 ||
		len(status.GetToolIds()) != 2 || status.GetToolIds()[0] != "docker" ||
		status.GetToolIds()[1] != "go" {
		t.Fatalf("status response = %#v", response)
	}
}

func TestRemoteHandlerRejectsUnavailableWorkerStatus(t *testing.T) {
	handler, err := NewRemoteHandler(remoteControllerStub{})
	if err != nil {
		t.Fatal(err)
	}
	response := handler.Handle(context.Background(), &computehopv1.RemoteRequest{
		Operation: &computehopv1.RemoteRequest_GetWorkerStatus{
			GetWorkerStatus: &computehopv1.GetWorkerStatusRequest{},
		},
	})
	if response.GetError().GetCode() != computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_CONFLICT {
		t.Fatalf("response = %#v", response)
	}
}

func TestRemoteHandlerReturnsReconnectableLogs(t *testing.T) {
	want := remoteHandlerJob(t)
	controller := remoteControllerStub{logs: func(
		_ context.Context,
		id job.ID,
		after uint64,
		limit int,
	) (JobLogs, error) {
		if id != want.ID || after != 4 || limit != 12 {
			t.Fatalf("read logs = %s, %d, %d", id, after, limit)
		}
		return JobLogs{Job: want, Page: joblogging.Page{
			Records: []joblogging.Record{{
				Sequence: 5, Stream: joblogging.StreamStdout, Data: []byte("hello\n"), At: want.UpdatedAt,
			}},
		}}, nil
	}}
	handler, err := NewRemoteHandler(controller)
	if err != nil {
		t.Fatal(err)
	}
	response := handler.Handle(context.Background(), &computehopv1.RemoteRequest{
		Operation: &computehopv1.RemoteRequest_ReadJobLogs{ReadJobLogs: &computehopv1.ReadJobLogsRequest{
			JobId: string(want.ID), AfterSequence: 4, Limit: 12,
		}},
	})
	result := response.GetReadJobLogs()
	if response.GetError() != nil || len(result.GetRecords()) != 1 ||
		result.GetRecords()[0].GetSequence() != 5 || string(result.GetRecords()[0].GetData()) != "hello\n" {
		t.Fatalf("response = %#v", response)
	}
}

func TestRemoteHandlerPreflightsUploadsAndSubmitsSnapshot(t *testing.T) {
	contents := []byte("package main\n")
	digest := snapshot.Sum(contents)
	manifest := snapshot.Manifest{
		Version: snapshot.ManifestVersion,
		Files: []snapshot.File{{
			Path: "src/main.go", Mode: 0o644, Size: int64(len(contents)),
			Chunks: []snapshot.Chunk{{Digest: digest, Size: uint32(len(contents))}},
		}},
		TotalBytes: int64(len(contents)),
	}
	manifestMessage, err := mapper.ManifestToRemoteProto(manifest)
	if err != nil {
		t.Fatal(err)
	}
	want := remoteHandlerJob(t)
	reserved := false
	released := false
	controller := &remoteSnapshotControllerStub{
		remoteControllerStub: remoteControllerStub{},
		missing: func(_ context.Context, digests []snapshot.Digest) ([]snapshot.Digest, error) {
			if len(digests) != 1 || digests[0] != digest {
				t.Fatalf("digests = %#v", digests)
			}
			return []snapshot.Digest{digest}, nil
		},
		put: func(_ context.Context, got snapshot.Digest, data []byte) error {
			if got != digest || string(data) != string(contents) {
				t.Fatalf("PutChunk() = %s, %q", got, data)
			}
			return nil
		},
		submitSnapshot: func(
			_ context.Context,
			spec job.Spec,
			got snapshot.Manifest,
			subdirectory string,
		) (job.Job, error) {
			gotID, _ := got.ID()
			wantID, _ := manifest.ID()
			if gotID != wantID || subdirectory != "src" || spec.Executable != "go" {
				t.Fatalf("SubmitSnapshot() = %#v, %s, %q", spec, gotID, subdirectory)
			}
			return want, nil
		},
		reserve: func(_ context.Context, got snapshot.Digest, digests []snapshot.Digest) error {
			manifestID, _ := manifest.ID()
			if got != manifestID || len(digests) != 1 || digests[0] != digest {
				t.Fatalf("ReserveSnapshot() = %s, %#v", got, digests)
			}
			reserved = true
			return nil
		},
		release: func(got snapshot.Digest) {
			manifestID, _ := manifest.ID()
			if got != manifestID {
				t.Fatalf("ReleaseSnapshot() = %s", got)
			}
			released = true
		},
	}
	handler, err := NewRemoteHandler(controller)
	if err != nil {
		t.Fatal(err)
	}
	manifestID, _ := manifest.ID()
	response := handler.Handle(context.Background(), &computehopv1.RemoteRequest{
		Operation: &computehopv1.RemoteRequest_CheckSnapshot{CheckSnapshot: &computehopv1.CheckSnapshotRequest{
			ManifestId: string(manifestID), ChunkDigests: []string{string(digest)},
		}},
	})
	if response.GetError() != nil || len(response.GetCheckSnapshot().GetMissingChunkDigests()) != 1 ||
		len(response.GetCheckSnapshot().GetAcceptedChunkEncodings()) != 2 {
		t.Fatalf("check response = %#v", response)
	}
	response = handler.Handle(context.Background(), &computehopv1.RemoteRequest{
		Operation: &computehopv1.RemoteRequest_PutChunk{PutChunk: &computehopv1.PutChunkRequest{
			Digest: string(digest), Data: contents,
			Encoding:         computehopv1.ChunkEncoding_CHUNK_ENCODING_IDENTITY,
			UncompressedSize: uint32(len(contents)),
		}},
	})
	if response.GetError() != nil || response.GetPutChunk().GetDigest() != string(digest) {
		t.Fatalf("put response = %#v", response)
	}
	response = handler.Handle(context.Background(), &computehopv1.RemoteRequest{
		Operation: &computehopv1.RemoteRequest_SubmitJob{SubmitJob: &computehopv1.SubmitJobRequest{
			Spec:     &computehopv1.JobSpec{Executable: "go", Executor: computehopv1.Executor_EXECUTOR_NATIVE},
			Snapshot: manifestMessage, WorkingSubdirectory: "src",
		}},
	})
	if response.GetError() != nil || response.GetSubmitJob().GetJob().GetId() != string(want.ID) {
		t.Fatalf("submit response = %#v", response)
	}
	if !reserved || !released {
		t.Fatalf("reserved = %t, released = %t", reserved, released)
	}
}

func TestRemoteHandlerRejectsMalformedCompressedChunkBeforeCaching(t *testing.T) {
	contents := bytes.Repeat([]byte("snapshot input\n"), 8_192)
	digest := snapshot.Sum(contents)
	encoded, err := transfer.EncodeChunk(contents, []transfer.ChunkEncoding{transfer.EncodingZstd})
	if err != nil {
		t.Fatal(err)
	}
	encoded.Data = append([]byte(nil), encoded.Data...)
	encoded.Data[len(encoded.Data)/2] ^= 0xff
	putCalled := false
	controller := &remoteSnapshotControllerStub{
		remoteControllerStub: remoteControllerStub{},
		put: func(context.Context, snapshot.Digest, []byte) error {
			putCalled = true
			return nil
		},
	}
	handler, err := NewRemoteHandler(controller)
	if err != nil {
		t.Fatal(err)
	}
	response := handler.Handle(context.Background(), &computehopv1.RemoteRequest{
		Operation: &computehopv1.RemoteRequest_PutChunk{PutChunk: &computehopv1.PutChunkRequest{
			Digest: string(digest), Data: encoded.Data,
			Encoding:         computehopv1.ChunkEncoding_CHUNK_ENCODING_ZSTD,
			UncompressedSize: encoded.UncompressedSize,
		}},
	})
	if response.GetError().GetCode() != computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_INVALID_ARGUMENT || putCalled {
		t.Fatalf("response = %#v; PutChunk called = %t", response, putCalled)
	}
}

func TestRemoteHandlerReturnsCompletedArtifactManifestAndAuthorizedChunk(t *testing.T) {
	contents := bytes.Repeat([]byte("rendered output\n"), 8_192)
	digest := snapshot.Sum(contents)
	want := remoteHandlerJob(t)
	want.State = job.StateSucceeded
	want.Spec.Outputs = []string{"dist/render.png"}
	bundle := artifact.Bundle{
		JobID: want.ID,
		Manifest: snapshot.Manifest{
			Version: snapshot.ManifestVersion,
			Files: []snapshot.File{{
				Path: "dist/render.png", Mode: 0o644, Size: int64(len(contents)),
				Chunks: []snapshot.Chunk{{Digest: digest, Size: uint32(len(contents))}},
			}},
			TotalBytes: int64(len(contents)),
		},
		CollectedAt: time.Unix(1_900_000_000, 0).UTC(),
	}
	acknowledged := false
	controller := &remoteArtifactControllerStub{
		remoteControllerStub: remoteControllerStub{},
		readArtifacts: func(_ context.Context, id job.ID) (JobArtifacts, error) {
			if id != want.ID {
				t.Fatalf("ReadArtifacts(%s)", id)
			}
			return JobArtifacts{Job: want, Bundle: bundle}, nil
		},
		readChunk: func(_ context.Context, id job.ID, got snapshot.Digest) ([]byte, error) {
			if id != want.ID || got != digest {
				t.Fatalf("ReadArtifactChunk(%s, %s)", id, got)
			}
			return contents, nil
		},
		markRetrieved: func(_ context.Context, id job.ID) error {
			if id != want.ID {
				t.Fatalf("MarkArtifactsRetrieved(%s)", id)
			}
			acknowledged = true
			return nil
		},
	}
	handler, err := NewRemoteHandler(controller)
	if err != nil {
		t.Fatal(err)
	}
	response := handler.Handle(context.Background(), &computehopv1.RemoteRequest{
		Operation: &computehopv1.RemoteRequest_GetJobArtifacts{GetJobArtifacts: &computehopv1.GetJobArtifactsRequest{
			JobId: string(want.ID),
		}},
	})
	result := response.GetGetJobArtifacts()
	if response.GetError() != nil || result.GetCollectedAtUnixNano() != bundle.CollectedAt.UnixNano() ||
		len(result.GetArtifacts().GetFiles()) != 1 {
		t.Fatalf("artifact response = %#v", response)
	}
	response = handler.Handle(context.Background(), &computehopv1.RemoteRequest{
		Operation: &computehopv1.RemoteRequest_GetArtifactChunk{GetArtifactChunk: &computehopv1.GetArtifactChunkRequest{
			JobId: string(want.ID), Digest: string(digest),
			AcceptedEncodings: []computehopv1.ChunkEncoding{
				computehopv1.ChunkEncoding_CHUNK_ENCODING_ZSTD,
				computehopv1.ChunkEncoding_CHUNK_ENCODING_IDENTITY,
			},
		}},
	})
	chunk := response.GetGetArtifactChunk()
	encoding, mapErr := mapper.ChunkEncodingFromRemoteProto(chunk.GetEncoding())
	decoded, decodeErr := transfer.DecodeChunk(transfer.Chunk{
		Encoding: encoding, Data: chunk.GetData(), UncompressedSize: chunk.GetUncompressedSize(),
	})
	if response.GetError() != nil || chunk.GetEncoding() != computehopv1.ChunkEncoding_CHUNK_ENCODING_ZSTD ||
		mapErr != nil || decodeErr != nil || !bytes.Equal(decoded, contents) {
		t.Fatalf("chunk response = %#v", response)
	}
	response = handler.Handle(context.Background(), &computehopv1.RemoteRequest{
		Operation: &computehopv1.RemoteRequest_AcknowledgeJobArtifacts{
			AcknowledgeJobArtifacts: &computehopv1.AcknowledgeJobArtifactsRequest{JobId: string(want.ID)},
		},
	})
	if response.GetError() != nil || response.GetAcknowledgeJobArtifacts().GetJobId() != string(want.ID) ||
		!acknowledged {
		t.Fatalf("acknowledgment response = %#v", response)
	}
}

func TestRemoteHandlerDoesNotLeakInternalErrors(t *testing.T) {
	controller := remoteControllerStub{get: func(context.Context, job.ID) (job.Job, error) {
		return job.Job{}, errors.New("database password was here")
	}}
	handler, err := NewRemoteHandler(controller)
	if err != nil {
		t.Fatal(err)
	}
	response := handler.Handle(context.Background(), &computehopv1.RemoteRequest{
		Operation: &computehopv1.RemoteRequest_GetJob{GetJob: &computehopv1.GetJobRequest{
			JobId: string(mustJobID(t, 99)),
		}},
	})
	if response.GetError().GetCode() != computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_INTERNAL ||
		response.GetError().GetMessage() != "internal worker error" {
		t.Fatalf("error = %#v", response.GetError())
	}
}

type remoteControllerStub struct {
	submit func(context.Context, job.Spec) (job.Job, error)
	get    func(context.Context, job.ID) (job.Job, error)
	list   func(context.Context, job.ListOptions) ([]job.Job, error)
	cancel func(context.Context, job.ID) (job.Job, error)
	logs   func(context.Context, job.ID, uint64, int) (JobLogs, error)
}

type remoteSnapshotControllerStub struct {
	remoteControllerStub
	missing        func(context.Context, []snapshot.Digest) ([]snapshot.Digest, error)
	put            func(context.Context, snapshot.Digest, []byte) error
	submitSnapshot func(context.Context, job.Spec, snapshot.Manifest, string) (job.Job, error)
	reserve        func(context.Context, snapshot.Digest, []snapshot.Digest) error
	release        func(snapshot.Digest)
}

func (stub *remoteSnapshotControllerStub) ReserveSnapshot(
	ctx context.Context,
	manifestID snapshot.Digest,
	digests []snapshot.Digest,
) error {
	if stub.reserve == nil {
		return nil
	}
	return stub.reserve(ctx, manifestID, digests)
}

func (stub *remoteSnapshotControllerStub) ReleaseSnapshot(manifestID snapshot.Digest) {
	if stub.release != nil {
		stub.release(manifestID)
	}
}

type remoteArtifactControllerStub struct {
	remoteControllerStub
	readArtifacts func(context.Context, job.ID) (JobArtifacts, error)
	readChunk     func(context.Context, job.ID, snapshot.Digest) ([]byte, error)
	markRetrieved func(context.Context, job.ID) error
}

func (stub *remoteArtifactControllerStub) MarkArtifactsRetrieved(
	ctx context.Context,
	id job.ID,
) error {
	if stub.markRetrieved == nil {
		return nil
	}
	return stub.markRetrieved(ctx, id)
}

func (stub *remoteArtifactControllerStub) ReadArtifacts(
	ctx context.Context,
	id job.ID,
) (JobArtifacts, error) {
	return stub.readArtifacts(ctx, id)
}

func (stub *remoteArtifactControllerStub) ReadArtifactChunk(
	ctx context.Context,
	id job.ID,
	digest snapshot.Digest,
) ([]byte, error) {
	return stub.readChunk(ctx, id, digest)
}

func (stub *remoteSnapshotControllerStub) MissingChunks(
	ctx context.Context,
	digests []snapshot.Digest,
) ([]snapshot.Digest, error) {
	return stub.missing(ctx, digests)
}

func (stub *remoteSnapshotControllerStub) PutChunk(
	ctx context.Context,
	digest snapshot.Digest,
	contents []byte,
) error {
	return stub.put(ctx, digest, contents)
}

func (stub *remoteSnapshotControllerStub) SubmitSnapshot(
	ctx context.Context,
	spec job.Spec,
	manifest snapshot.Manifest,
	workingSubdirectory string,
) (job.Job, error) {
	return stub.submitSnapshot(ctx, spec, manifest, workingSubdirectory)
}

func (stub remoteControllerStub) Submit(ctx context.Context, spec job.Spec) (job.Job, error) {
	return stub.submit(ctx, spec)
}

func (stub remoteControllerStub) Get(ctx context.Context, id job.ID) (job.Job, error) {
	return stub.get(ctx, id)
}

func (stub remoteControllerStub) List(ctx context.Context, options job.ListOptions) ([]job.Job, error) {
	return stub.list(ctx, options)
}

func (stub remoteControllerStub) Cancel(ctx context.Context, id job.ID) (job.Job, error) {
	return stub.cancel(ctx, id)
}

func (stub remoteControllerStub) ReadLogs(
	ctx context.Context,
	id job.ID,
	after uint64,
	limit int,
) (JobLogs, error) {
	return stub.logs(ctx, id, after, limit)
}

func remoteHandlerJob(t *testing.T) job.Job {
	t.Helper()
	now := time.Unix(1_700_000_000, 0).UTC()
	return job.Job{
		ID: mustJobID(t, 42),
		Spec: job.Spec{
			Executable: "echo", Arguments: []string{"hello"}, WorkingDirectory: "/worker/project",
			Executor: job.ExecutorNative,
		},
		State: job.StateQueued, CreatedAt: now, UpdatedAt: now.Add(time.Second),
	}
}
