// Copyright (c) 2025 OpenCSG
// SPDX-License-Identifier: MIT
//
// Package gitalyclient 封装 Gitaly gRPC 客户端，提供仓库操作接口。
package gitalyclient

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"path"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

// Client wraps Gitaly gRPC connections and provides repository operations.
type Client struct {
	conn *grpc.ClientConn

	commitClient    gitalypb.CommitServiceClient
	blobClient      gitalypb.BlobServiceClient
	refClient       gitalypb.RefServiceClient
	operationClient gitalypb.OperationServiceClient
}

// Config holds connection and repository configuration.
type Config struct {
	Address      string // Gitaly address (e.g. localhost:8075 or unix:///path/to/socket)
	StorageName  string
	RelativePath string
	Branch       string // empty = use FindDefaultBranchName
	User         *gitalypb.User
	Token        string // optional: Bearer token for gRPC metadata (env: GITALY_TOKEN)
}

// NewClient creates a new Gitaly client.
func NewClient(ctx context.Context, cfg *Config) (*Client, error) {
	var opts []grpc.DialOption
	opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if cfg.Token != "" {
		opts = append(opts, grpc.WithUnaryInterceptor(authUnaryInterceptor(cfg.Token)))
		opts = append(opts, grpc.WithStreamInterceptor(authStreamInterceptor(cfg.Token)))
	}

	conn, err := grpc.DialContext(ctx, strings.TrimPrefix(cfg.Address, "unix://"), opts...)
	if err != nil {
		return nil, fmt.Errorf("dial gitaly: %w", err)
	}

	return &Client{
		conn:            conn,
		commitClient:    gitalypb.NewCommitServiceClient(conn),
		blobClient:      gitalypb.NewBlobServiceClient(conn),
		refClient:       gitalypb.NewRefServiceClient(conn),
		operationClient: gitalypb.NewOperationServiceClient(conn),
	}, nil
}

// Close closes the gRPC connection.
func (c *Client) Close() error {
	return c.conn.Close()
}

func authUnaryInterceptor(token string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

func authStreamInterceptor(token string) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
		return streamer(ctx, desc, cc, method, opts...)
	}
}

// Repo returns a *gitalypb.Repository for the given storage and path.
func Repo(storageName, relativePath string) *gitalypb.Repository {
	return &gitalypb.Repository{
		StorageName:  storageName,
		RelativePath: relativePath,
	}
}

// DefaultBranch returns the default branch name for the repository.
func (c *Client) DefaultBranch(ctx context.Context, repo *gitalypb.Repository) (string, error) {
	log.Printf("[gitaly] DefaultBranch repo=%s", repo.GetRelativePath())
	resp, err := c.refClient.FindDefaultBranchName(ctx, &gitalypb.FindDefaultBranchNameRequest{
		Repository: repo,
	})
	if err != nil {
		log.Printf("[gitaly] DefaultBranch error: %v", err)
		return "", fmt.Errorf("find default branch: %w", err)
	}
	name := string(resp.GetName())
	log.Printf("[gitaly] DefaultBranch ok => %s", name)
	return name, nil
}

// GetTreeEntries returns tree entries for the given path at revision.
// Use "." for repo root (empty path is rejected by some Gitaly versions).
func (c *Client) GetTreeEntries(ctx context.Context, repo *gitalypb.Repository, revision, repoPath string, recursive bool) ([]*gitalypb.TreeEntry, error) {
	if repoPath == "" {
		repoPath = "."
	}
	log.Printf("[gitaly] GetTreeEntries repo=%s revision=%s path=%s recursive=%v", repo.GetRelativePath(), revision, repoPath, recursive)
	stream, err := c.commitClient.GetTreeEntries(ctx, &gitalypb.GetTreeEntriesRequest{
		Repository: repo,
		Revision:   []byte(revision),
		Path:       []byte(repoPath),
		Recursive:  recursive,
		Sort:       gitalypb.GetTreeEntriesRequest_TREES_FIRST,
	})
	if err != nil {
		log.Printf("[gitaly] GetTreeEntries error: %v", err)
		return nil, fmt.Errorf("get tree entries: %w", err)
	}

	var entries []*gitalypb.TreeEntry
	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Printf("[gitaly] GetTreeEntries recv error: %v", err)
			return nil, fmt.Errorf("recv tree entries: %w", err)
		}
		entries = append(entries, resp.GetEntries()...)
	}
	log.Printf("[gitaly] GetTreeEntries ok entries=%d", len(entries))
	return entries, nil
}

// GetTreeEntry reads a single tree entry (file or dir metadata + blob data for files).
func (c *Client) GetTreeEntry(ctx context.Context, repo *gitalypb.Repository, revision, repoPath string, limit int64) (typ gitalypb.TreeEntryResponse_ObjectType, oid string, size int64, mode int32, data []byte, err error) {
	if repoPath == "" {
		repoPath = "."
	}
	log.Printf("[gitaly] GetTreeEntry repo=%s revision=%s path=%s limit=%d", repo.GetRelativePath(), revision, repoPath, limit)
	stream, err := c.commitClient.TreeEntry(ctx, &gitalypb.TreeEntryRequest{
		Repository: repo,
		Revision:   []byte(revision),
		Path:       []byte(repoPath),
		Limit:      limit,
	})
	if err != nil {
		log.Printf("[gitaly] GetTreeEntry error: %v", err)
		return 0, "", 0, 0, nil, fmt.Errorf("tree entry: %w", err)
	}

	var buf bytes.Buffer
	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Printf("[gitaly] GetTreeEntry recv error: %v", err)
			return 0, "", 0, 0, nil, fmt.Errorf("recv tree entry: %w", err)
		}
		if oid == "" {
			oid = resp.GetOid()
			size = resp.GetSize()
			mode = resp.GetMode()
			typ = resp.GetType()
			if size > 0 && limit < 0 {
				buf.Grow(int(size))
			}
		}
		buf.Write(resp.GetData())
	}
	log.Printf("[gitaly] GetTreeEntry ok path=%s oid=%s size=%d", repoPath, oid, len(buf.Bytes()))
	return typ, oid, size, mode, buf.Bytes(), nil
}

// GetBlob reads blob content by OID.
func (c *Client) GetBlob(ctx context.Context, repo *gitalypb.Repository, oid string, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = -1
	}
	log.Printf("[gitaly] GetBlob repo=%s oid=%s limit=%d", repo.GetRelativePath(), oid, limit)
	stream, err := c.blobClient.GetBlob(ctx, &gitalypb.GetBlobRequest{
		Repository: repo,
		Oid:        oid,
		Limit:      limit,
	})
	if err != nil {
		log.Printf("[gitaly] GetBlob error: %v", err)
		return nil, fmt.Errorf("get blob: %w", err)
	}

	var buf bytes.Buffer
	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Printf("[gitaly] GetBlob recv error: %v", err)
			return nil, fmt.Errorf("recv blob: %w", err)
		}
		buf.Write(resp.GetData())
	}
	log.Printf("[gitaly] GetBlob ok oid=%s size=%d", oid, buf.Len())
	return buf.Bytes(), nil
}

// Action represents a UserCommitFiles action.
type Action struct {
	Type         gitalypb.UserCommitFilesActionHeader_ActionType
	FilePath     string
	PreviousPath string // for MOVE
	Content      []byte // for CREATE, UPDATE
}

// UserCommitFiles performs a commit with the given actions.
func (c *Client) UserCommitFiles(ctx context.Context, repo *gitalypb.Repository, branch, message string, user *gitalypb.User, actions ...Action) (*gitalypb.OperationBranchUpdate, error) {
	if user == nil {
		user = &gitalypb.User{Name: []byte("fuse"), Email: []byte("fuse@local")}
	}
	log.Printf("[gitaly] UserCommitFiles repo=%s branch=%s message=%q actions=%d", repo.GetRelativePath(), branch, message, len(actions))
	stream, err := c.operationClient.UserCommitFiles(ctx)
	if err != nil {
		log.Printf("[gitaly] UserCommitFiles stream error: %v", err)
		return nil, fmt.Errorf("user commit files stream: %w", err)
	}

	err = stream.Send(&gitalypb.UserCommitFilesRequest{
		UserCommitFilesRequestPayload: &gitalypb.UserCommitFilesRequest_Header{
			Header: &gitalypb.UserCommitFilesRequestHeader{
				Repository:        repo,
				User:              user,
				BranchName:        []byte(branch),
				CommitMessage:     []byte(message),
				CommitAuthorName:  user.Name,
				CommitAuthorEmail: user.Email,
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("send header: %w", err)
	}

	for _, a := range actions {
		header := &gitalypb.UserCommitFilesActionHeader{
			Action:       a.Type,
			FilePath:     []byte(a.FilePath),
			PreviousPath: []byte(a.PreviousPath),
		}
		err = stream.Send(&gitalypb.UserCommitFilesRequest{
			UserCommitFilesRequestPayload: &gitalypb.UserCommitFilesRequest_Action{
				Action: &gitalypb.UserCommitFilesAction{
					UserCommitFilesActionPayload: &gitalypb.UserCommitFilesAction_Header{
						Header: header,
					},
				},
			},
		})
		if err != nil {
			return nil, fmt.Errorf("send action header: %w", err)
		}
		if len(a.Content) > 0 {
			err = stream.Send(&gitalypb.UserCommitFilesRequest{
				UserCommitFilesRequestPayload: &gitalypb.UserCommitFilesRequest_Action{
					Action: &gitalypb.UserCommitFilesAction{
						UserCommitFilesActionPayload: &gitalypb.UserCommitFilesAction_Content{
							Content: a.Content,
						},
					},
				},
			})
			if err != nil {
				return nil, fmt.Errorf("send action content: %w", err)
			}
		}
	}

	resp, err := stream.CloseAndRecv()
	if err != nil {
		log.Printf("[gitaly] UserCommitFiles close/recv error: %v", err)
		return nil, fmt.Errorf("close and recv: %w", err)
	}
	if resp.GetIndexError() != "" {
		log.Printf("[gitaly] UserCommitFiles index_error: %s", resp.GetIndexError())
		return nil, fmt.Errorf("index error: %s", resp.GetIndexError())
	}
	if resp.GetPreReceiveError() != "" {
		log.Printf("[gitaly] UserCommitFiles pre_receive_error: %s", resp.GetPreReceiveError())
		return nil, fmt.Errorf("pre-receive error: %s", resp.GetPreReceiveError())
	}
	log.Printf("[gitaly] UserCommitFiles ok")
	return resp.GetBranchUpdate(), nil
}

// JoinPath joins repo path components (handles empty base).
func JoinPath(base, name string) string {
	if base == "" {
		return name
	}
	return path.Join(base, name)
}
