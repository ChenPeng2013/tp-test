package main

import (
	"context"
	"database/sql"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/spf13/cobra"
)

func rootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "tp-test",
	}
	cmd.AddCommand(playCmd())
	return cmd
}

func playCmd() *cobra.Command {
	var opts playOptions
	cmd := &cobra.Command{
		Use:           "play",
		Short:         "Generate & run tests",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			var (
				yy   []byte
				resp *http.Response
			)
			if strings.HasPrefix(args[0], "http") {
				resp, err = http.Get(args[0])
				if err != nil {
					return err
				}
				yy, err = ioutil.ReadAll(resp.Body)
				resp.Body.Close()
			} else {
				yy, err = ioutil.ReadFile(args[0])
			}
			if err != nil {
				return err
			}
			opts.Grammar = string(yy)
			opts.DSN1 = processDSN(opts.DSN1)
			opts.DSN1 = ignoreInvalidVar(opts.DSN1)
			opts.DSN2 = processDSN(opts.DSN2)
			opts.DSN2 = ignoreInvalidVar(opts.DSN2)
			return play(opts)
		},
	}
	cmd.Flags().StringVar(&opts.InitRoot, "init-root", "init", "entry rule of initialization sql")
	cmd.Flags().StringVar(&opts.TestRoot, "test-root", "test", "entry rule of test sql")
	cmd.Flags().IntVar(&opts.RecurLimit, "recur-limit", 15, "max recursion level for sql generation")
	cmd.Flags().IntVar(&opts.Tests, "tests", 5, "number of tests for each round")
	cmd.Flags().BoolVar(&opts.Debug, "debug", false, "enable debug option of generator")
	cmd.Flags().Int64Var(&opts.Rounds, "rounds", 1, "number of rounds to execute")
	cmd.Flags().IntVar(&opts.Threads, "threads", 1, "number of worker threads")
	cmd.Flags().StringVar(&opts.DSN1, "dsn1", "", "dsn for 1st database")
	cmd.Flags().StringVar(&opts.DSN2, "dsn2", "", "dsn for 2nd database")
	cmd.Flags().StringVar(&opts.DBName, "db", "tp_test", "basename of database")
	cmd.Flags().StringVar(&opts.OutDir, "out", "tp_test_out.d", "directory to dump failures")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "only print generated queries")
	cmd.Flags().DurationVar(&opts.LockThreshold, "lock-threshold", 3*time.Second, "lock threshold")
	return cmd
}

func processDSN(dsn string) string {
	if !strings.HasPrefix(dsn, "mysql://") {
		return dsn
	}
	a, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	var auth string
	if a.User != nil {
		pass, _ := a.User.Password()
		auth = a.User.Username() + ":" + pass
	} else {
		auth = "root:"
	}
	return fmt.Sprintf("%s@tcp(%s)/?%s", auth, a.Host, a.RawQuery)
}

func ignoreInvalidVar(dsn string) string {
	c, err := mysql.ParseDSN(dsn)
	if err != nil {
		return dsn
	}
	tmpMap := c.Params
	c.Params = nil
	db, err := sql.Open("mysql", c.FormatDSN())
	defer db.Close()

	c.Params = tmpMap
	for key := range c.Params {
		_, err := db.ExecContext(context.Background(), "select @@"+key)
		if err != nil && strings.Contains(err.Error(), "Unknown system variable") {
			delete(c.Params, key)
		}
	}
	newDsn := c.FormatDSN()
	log.Printf("dsn %s\n", dsn)
	log.Printf("newDsn %s\n", newDsn)
	return newDsn
}
