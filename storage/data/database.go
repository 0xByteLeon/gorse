// Copyright 2021 gorse Project Authors
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

package data

import (
	"context"
	"database/sql"
	"github.com/dzwvip/oracle"
	"github.com/go-redis/redis/v8"
	"github.com/juju/errors"
	"github.com/samber/lo"
	"github.com/zhenghaoz/gorse/base/log"
	"github.com/zhenghaoz/gorse/storage"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/x/mongo/driver/connstring"
	"gorm.io/driver/clickhouse"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"moul.io/zapgorm2"
	"net/url"
	"sort"
	"strings"
	"time"
)

var (
	ErrUserNotExist = errors.NotFoundf("user")
	ErrItemNotExist = errors.NotFoundf("item")
	ErrNoDatabase   = errors.NotAssignedf("database")
)

// Item stores meta data about item.
type Item struct {
	ItemId     string `gorm:"primaryKey"`
	IsHidden   bool
	Categories []string `gorm:"serializer:json"`
	Timestamp  time.Time
	Labels     []string `gorm:"serializer:json"`
	Comment    string
}

// ItemPatch is the modification on an item.
type ItemPatch struct {
	IsHidden   *bool
	Categories []string
	Timestamp  *time.Time
	Labels     []string
	Comment    *string
}

// User stores meta data about user.
type User struct {
	UserId    string   `gorm:"primaryKey"`
	Labels    []string `gorm:"serializer:json"`
	Subscribe []string `gorm:"serializer:json"`
	Comment   string
}

// UserPatch is the modification on a user.
type UserPatch struct {
	Labels    []string
	Subscribe []string
	Comment   *string
}

// FeedbackKey identifies feedback.
type FeedbackKey struct {
	FeedbackType string `gorm:"column:feedback_type"`
	UserId       string `gorm:"column:user_id"`
	ItemId       string `gorm:"column:item_id"`
}

// Feedback stores feedback.
type Feedback struct {
	FeedbackKey `gorm:"embedded"`
	Timestamp   time.Time `gorm:"column:time_stamp"`
	Comment     string    `gorm:"column:comment"`
}

// SortFeedbacks sorts feedback from latest to oldest.
func SortFeedbacks(feedback []Feedback) {
	sort.Sort(feedbackSorter(feedback))
}

type feedbackSorter []Feedback

func (sorter feedbackSorter) Len() int {
	return len(sorter)
}

func (sorter feedbackSorter) Less(i, j int) bool {
	return sorter[i].Timestamp.After(sorter[j].Timestamp)
}

func (sorter feedbackSorter) Swap(i, j int) {
	sorter[i], sorter[j] = sorter[j], sorter[i]
}

type Database interface {
	Init() error
	Close() error
	Optimize() error
	Purge() error
	BatchInsertItems(items []Item) error
	BatchGetItems(itemIds []string) ([]Item, error)
	DeleteItem(itemId string) error
	GetItem(itemId string) (Item, error)
	ModifyItem(itemId string, patch ItemPatch) error
	GetItems(cursor string, n int, timeLimit *time.Time) (string, []Item, error)
	GetItemFeedback(itemId string, feedbackTypes ...string) ([]Feedback, error)
	BatchInsertUsers(users []User) error
	DeleteUser(userId string) error
	GetUser(userId string) (User, error)
	ModifyUser(userId string, patch UserPatch) error
	GetUsers(cursor string, n int) (string, []User, error)
	GetUserFeedback(userId string, withFuture bool, feedbackTypes ...string) ([]Feedback, error)
	GetUserItemFeedback(userId, itemId string, feedbackTypes ...string) ([]Feedback, error)
	DeleteUserItemFeedback(userId, itemId string, feedbackTypes ...string) (int, error)
	BatchInsertFeedback(feedback []Feedback, insertUser, insertItem, overwrite bool) error
	GetFeedback(cursor string, n int, timeLimit *time.Time, feedbackTypes ...string) (string, []Feedback, error)
	GetUserStream(batchSize int) (chan []User, chan error)
	GetItemStream(batchSize int, timeLimit *time.Time) (chan []Item, chan error)
	GetFeedbackStream(batchSize int, timeLimit *time.Time, feedbackTypes ...string) (chan []Feedback, chan error)
}

// Open a connection to a database.
func Open(path, tablePrefix string) (Database, error) {
	var err error
	if strings.HasPrefix(path, storage.MySQLPrefix) {
		name := path[len(storage.MySQLPrefix):]
		// probe isolation variable name
		isolationVarName, err := storage.ProbeMySQLIsolationVariableName(name)
		if err != nil {
			return nil, errors.Trace(err)
		}
		// append parameters
		if name, err = storage.AppendMySQLParams(name, map[string]string{
			"sql_mode":       "'ONLY_FULL_GROUP_BY,STRICT_TRANS_TABLES,ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION'",
			isolationVarName: "'READ-UNCOMMITTED'",
			"parseTime":      "true",
		}); err != nil {
			return nil, errors.Trace(err)
		}
		// connect to database
		database := new(SQLDatabase)
		database.driver = MySQL
		database.TablePrefix = storage.TablePrefix(tablePrefix)
		if database.client, err = sql.Open("mysql", name); err != nil {
			return nil, errors.Trace(err)
		}
		database.gormDB, err = gorm.Open(mysql.New(mysql.Config{Conn: database.client}), storage.NewGORMConfig(tablePrefix))
		if err != nil {
			return nil, errors.Trace(err)
		}
		return database, nil
	} else if strings.HasPrefix(path, storage.PostgresPrefix) || strings.HasPrefix(path, storage.PostgreSQLPrefix) {
		database := new(SQLDatabase)
		database.driver = Postgres
		database.TablePrefix = storage.TablePrefix(tablePrefix)
		if database.client, err = sql.Open("postgres", path); err != nil {
			return nil, errors.Trace(err)
		}
		database.gormDB, err = gorm.Open(postgres.New(postgres.Config{Conn: database.client}), storage.NewGORMConfig(tablePrefix))
		if err != nil {
			return nil, errors.Trace(err)
		}
		return database, nil
	} else if strings.HasPrefix(path, storage.ClickhousePrefix) || strings.HasPrefix(path, storage.CHHTTPPrefix) || strings.HasPrefix(path, storage.CHHTTPSPrefix) {
		// replace schema
		parsed, err := url.Parse(path)
		if err != nil {
			return nil, errors.Trace(err)
		}
		if strings.HasPrefix(path, storage.CHHTTPSPrefix) {
			parsed.Scheme = "https"
		} else {
			parsed.Scheme = "http"
		}
		uri := parsed.String()
		database := new(SQLDatabase)
		database.driver = ClickHouse
		database.TablePrefix = storage.TablePrefix(tablePrefix)
		if database.client, err = sql.Open("chhttp", uri); err != nil {
			return nil, errors.Trace(err)
		}
		database.gormDB, err = gorm.Open(clickhouse.New(clickhouse.Config{Conn: database.client}), storage.NewGORMConfig(tablePrefix))
		if err != nil {
			return nil, errors.Trace(err)
		}
		return database, nil
	} else if strings.HasPrefix(path, storage.MongoPrefix) || strings.HasPrefix(path, storage.MongoSrvPrefix) {
		// connect to database
		database := new(MongoDB)
		if database.client, err = mongo.Connect(context.Background(), options.Client().ApplyURI(path)); err != nil {
			return nil, errors.Trace(err)
		}
		// parse DSN and extract database name
		if cs, err := connstring.ParseAndValidate(path); err != nil {
			return nil, errors.Trace(err)
		} else {
			database.dbName = cs.Database
			database.TablePrefix = storage.TablePrefix(tablePrefix)
		}
		return database, nil
	} else if strings.HasPrefix(path, storage.SQLitePrefix) {
		// append parameters
		if path, err = storage.AppendURLParams(path, []lo.Tuple2[string, string]{
			{"_pragma", "busy_timeout(10000)"},
			{"_pragma", "journal_mode(wal)"},
		}); err != nil {
			return nil, errors.Trace(err)
		}
		// connect to database
		name := path[len(storage.SQLitePrefix):]
		database := new(SQLDatabase)
		database.driver = SQLite
		database.TablePrefix = storage.TablePrefix(tablePrefix)
		if database.client, err = sql.Open("sqlite", name); err != nil {
			return nil, errors.Trace(err)
		}
		gormConfig := storage.NewGORMConfig(tablePrefix)
		gormConfig.Logger = &zapgorm2.Logger{
			ZapLogger:                 log.Logger(),
			LogLevel:                  logger.Warn,
			SlowThreshold:             10 * time.Second,
			SkipCallerLookup:          false,
			IgnoreRecordNotFoundError: false,
		}
		database.gormDB, err = gorm.Open(sqlite.Dialector{Conn: database.client}, gormConfig)
		if err != nil {
			return nil, errors.Trace(err)
		}
		return database, nil
	} else if strings.HasPrefix(path, storage.RedisPrefix) {
		if strings.Contains(path, ",") {
			addrsSlice := strings.Split(path, ",")
			var addrs []string
			for _, addr := range addrsSlice {
				if addr != "" {
					addrs = append(addrs, addr)
				}
			}
			dbCluster := new(RedisCluster)
			clusterOpt := redis.ClusterOptions{
				Addrs: addrs,
			}
			dbCluster.client = redis.NewClusterClient(&clusterOpt)
			return dbCluster, nil
		} else {
			addr := path[len(storage.RedisPrefix):]
			database := new(Redis)
			database.client = redis.NewClient(&redis.Options{Addr: addr})
			if tablePrefix != "" {
				panic("table prefix is not supported for redis")
			}
			log.Logger().Warn("redis is used for testing only")
			return database, nil
		}
	} else if strings.HasPrefix(path, storage.OraclePrefix) {
		database := new(SQLDatabase)
		database.driver = Oracle
		database.TablePrefix = storage.TablePrefix(tablePrefix)
		if database.client, err = sql.Open("oracle", path); err != nil {
			return nil, errors.Trace(err)
		}
		database.gormDB, err = gorm.Open(oracle.New(oracle.Config{Conn: database.client}), storage.NewGORMConfig(tablePrefix))
		if err != nil {
			return nil, errors.Trace(err)
		}
		return database, nil
	}
	return nil, errors.Errorf("Unknown database: %s", path)
}
