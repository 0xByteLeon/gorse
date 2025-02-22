//go:build integrate_test

// Copyright 2022 gorse Project Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package client

import (
	"context"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/suite"
	"testing"
	"time"
)

const (
	RedisEndpoint = "redis://127.0.0.1:6379/0"
	GorseEndpoint = "http://127.0.0.1:8087"
	GorseApiKey   = ""
)

type GorseClientTestSuite struct {
	suite.Suite
	client *GorseClient
	redis  *redis.Client
}

func (suite *GorseClientTestSuite) SetupSuite() {
	suite.client = NewGorseClient(GorseEndpoint, GorseApiKey)
	options, err := redis.ParseURL(RedisEndpoint)
	suite.NoError(err)
	suite.redis = redis.NewClient(options)
}

func (suite *GorseClientTestSuite) TearDownSuite() {
	err := suite.redis.Close()
	suite.NoError(err)
}

func (suite *GorseClientTestSuite) TestFeedback() {
	timestamp := time.Unix(1660459054, 0).UTC().Format(time.RFC3339)
	userId := "800"
	insertFeedbackResp, err := suite.client.InsertFeedback([]Feedback{{
		FeedbackType: "like",
		UserId:       userId,
		Timestamp:    timestamp,
		ItemId:       "200",
	}})
	suite.NoError(err)
	suite.Equal(1, insertFeedbackResp.RowAffected)

	insertFeedbacksResp, err := suite.client.InsertFeedback([]Feedback{{
		FeedbackType: "read",
		UserId:       userId,
		Timestamp:    timestamp,
		ItemId:       "300",
	}, {
		FeedbackType: "read",
		UserId:       userId,
		Timestamp:    timestamp,
		ItemId:       "400",
	}})
	suite.NoError(err)
	suite.Equal(2, insertFeedbacksResp.RowAffected)

	feedbacks, err := suite.client.ListFeedbacks("read", userId)
	suite.NoError(err)
	suite.ElementsMatch([]Feedback{
		{
			FeedbackType: "read",
			UserId:       userId,
			Timestamp:    timestamp,
			ItemId:       "300",
		}, {
			FeedbackType: "read",
			UserId:       userId,
			Timestamp:    timestamp,
			ItemId:       "400",
		},
	}, feedbacks)
}

func (suite *GorseClientTestSuite) TestRecommend() {
	suite.redis.ZAddArgs(context.Background(), "offline_recommend/100", redis.ZAddArgs{
		Members: []redis.Z{
			{
				Score:  1,
				Member: "1",
			},
			{
				Score:  2,
				Member: "2",
			},
			{
				Score:  3,
				Member: "3",
			},
		},
	})
	resp, err := suite.client.GetRecommend("100", "", 10)
	suite.NoError(err)
	suite.Equal([]string{"3", "2", "1"}, resp)
}

func (suite *GorseClientTestSuite) TestSessionRecommend() {
	ctx := context.Background()
	suite.redis.ZAddArgs(ctx, "item_neighbors/1", redis.ZAddArgs{
		Members: []redis.Z{
			{
				Score:  100000,
				Member: "2",
			},
			{
				Score:  1,
				Member: "9",
			},
		},
	})
	suite.redis.ZAddArgs(ctx, "item_neighbors/2", redis.ZAddArgs{
		Members: []redis.Z{
			{
				Score:  100000,
				Member: "3",
			},
			{
				Score:  1,
				Member: "8",
			},
			{
				Score:  1,
				Member: "9",
			},
		},
	})
	suite.redis.ZAddArgs(ctx, "item_neighbors/3", redis.ZAddArgs{
		Members: []redis.Z{
			{
				Score:  100000,
				Member: "4",
			},
			{
				Score:  1,
				Member: "7",
			},
			{
				Score:  1,
				Member: "8",
			},
			{
				Score:  1,
				Member: "9",
			},
		},
	})
	suite.redis.ZAddArgs(ctx, "item_neighbors/4", redis.ZAddArgs{
		Members: []redis.Z{
			{
				Score:  100000,
				Member: "1",
			},
			{
				Score:  1,
				Member: "6",
			},
			{
				Score:  1,
				Member: "7",
			},
			{
				Score:  1,
				Member: "8",
			},
			{
				Score:  1,
				Member: "9",
			},
		},
	})

	feedbackType := "like"
	userId := "0"
	timestamp := time.Unix(1660459054, 0).UTC().Format(time.RFC3339)
	resp, err := suite.client.SessionRecommend([]Feedback{
		{
			FeedbackType: feedbackType,
			UserId:       userId,
			ItemId:       "1",
			Timestamp:    timestamp,
		},
		{
			FeedbackType: feedbackType,
			UserId:       userId,
			ItemId:       "2",
			Timestamp:    timestamp,
		},
		{
			FeedbackType: feedbackType,
			UserId:       userId,
			ItemId:       "3",
			Timestamp:    timestamp,
		},
		{
			FeedbackType: feedbackType,
			UserId:       userId,
			ItemId:       "4",
			Timestamp:    timestamp,
		},
	}, 3)
	suite.NoError(err)
	suite.Equal([]Score{
		{
			Id:    "9",
			Score: 4,
		},
		{
			Id:    "8",
			Score: 3,
		},
		{
			Id:    "7",
			Score: 2,
		},
	}, resp)
}

func (suite *GorseClientTestSuite) TestNeighbors() {
	r := redis.NewClient(&redis.Options{
		Addr: "127.0.0.1:6379",
		DB:   0,
	})
	ctx := context.Background()
	r.ZAddArgs(ctx, "item_neighbors/100", redis.ZAddArgs{
		Members: []redis.Z{
			{
				Score:  1,
				Member: "1",
			},
			{
				Score:  2,
				Member: "2",
			},
			{
				Score:  3,
				Member: "3",
			},
		},
	})

	itemId := "100"
	resp, err := suite.client.GetNeighbors(itemId, 3)
	suite.NoError(err)
	suite.Equal([]Score{
		{
			Id:    "3",
			Score: 3,
		}, {
			Id:    "2",
			Score: 2,
		}, {
			Id:    "1",
			Score: 1,
		},
	}, resp)
}

func (suite *GorseClientTestSuite) TestUsers() {
	user := User{
		UserId:    "100",
		Labels:    []string{"a", "b", "c"},
		Subscribe: []string{"d", "e"},
		Comment:   "comment",
	}
	rowAffected, err := suite.client.InsertUser(user)
	suite.NoError(err)
	suite.Equal(1, rowAffected.RowAffected)

	userResp, err := suite.client.GetUser("100")
	suite.NoError(err)
	suite.Equal(user, userResp)

	deleteAffect, err := suite.client.DeleteUser("100")
	suite.NoError(err)
	suite.Equal(1, deleteAffect.RowAffected)

	_, err = suite.client.GetUser("100")
	suite.Equal("100: user not found", err.Error())
}

func (suite *GorseClientTestSuite) TestItems() {
	timestamp := time.Unix(1660459054, 0).UTC().Format(time.RFC3339)
	item := Item{
		ItemId:     "100",
		IsHidden:   true,
		Labels:     []string{"a", "b", "c"},
		Categories: []string{"d", "e"},
		Timestamp:  timestamp,
		Comment:    "comment",
	}
	rowAffected, err := suite.client.InsertItem(item)
	suite.NoError(err)
	suite.Equal(1, rowAffected.RowAffected)

	itemResp, err := suite.client.GetItem("100")
	suite.NoError(err)
	suite.Equal(item, itemResp)

	deleteAffect, err := suite.client.DeleteItem("100")
	suite.NoError(err)
	suite.Equal(1, deleteAffect.RowAffected)

	_, err = suite.client.GetItem("100")
	suite.Equal("100: item not found", err.Error())
}

func TestGorseClientTestSuite(t *testing.T) {
	suite.Run(t, new(GorseClientTestSuite))
}
