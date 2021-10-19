package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"math"
	"os"
	"path/filepath"

	"github.com/fatih/color"
	"github.com/pingcap/go-randgen/compare"
	"github.com/pingcap/go-randgen/gendata"
	"github.com/pingcap/go-randgen/grammar/sqlgen"
	"github.com/sergi/go-diff/diffmatchpatch"
	"github.com/spf13/cobra"
)

var dsn1 string
var dsn2 string
var order bool
var dumpDir string

func newExecCmd() *cobra.Command {
	execCmd := &cobra.Command{
		Use:   "exec",
		Short: "exec sql in two dsn and compare their result",
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if yyPath == "" {
				return errors.New("yy are required")
			}
			if dsn1 == "" || dsn2 == "" {
				return errors.New("dsn must have a pair")
			}

			if maxRecursive <= 0 {
				maxRecursive = math.MaxInt32
			}

			return nil
		},
		Run: execAction,
	}

	execCmd.Flags().StringVar(&dsn1, "dsn1", "", "one of compare mysql dsn")
	execCmd.Flags().StringVar(&dsn2, "dsn2", "", "another compare mysql dsn")
	execCmd.Flags().BoolVar(&order, "order",
		false, "compare sql result with order")
	execCmd.Flags().StringVar(&dumpDir, "dump",
		"dump", "inconsistent sqls dump directory")

	return execCmd
}

type dumpInfo struct {
	num     int // serial number
	sql     string
	dsn1    string
	dsn2    string
	dsn1Res compare.DsnRes
	dsn2Res compare.DsnRes
}

func (dump *dumpInfo) String() string {
	bs := &bytes.Buffer{}
	dsn1Tag := fmt.Sprintf("[[%s]]\n\n", dump.dsn1)
	dsn2Tag := fmt.Sprintf("[[%s]]\n\n", dump.dsn2)

	// [sql]
	bs.WriteString("[sql]\n\n")
	bs.WriteString(dump.sql + "\n\n")

	// [err]
	bs.WriteString("[err]\n\n")
	bs.WriteString(dsn1Tag)
	if dump.dsn1Res.Err() != nil {
		bs.WriteString(dump.dsn1Res.Err().Error() + "\n\n")
	}
	bs.WriteString(dsn2Tag)
	if dump.dsn2Res.Err() != nil {
		bs.WriteString(dump.dsn2Res.Err().Error() + "\n\n")
	}

	// [compare]
	bs.WriteString("[compare]\n\n")
	dsn1Colored, dsn2Colored := getColorDiff(dump.dsn1Res.String(),
		dump.dsn2Res.String())
	bs.WriteString(dsn1Tag)
	bs.WriteString(dsn1Colored + "\n\n")
	bs.WriteString(dsn2Tag)
	bs.WriteString(dsn2Colored)

	return bs.String()
}

// dump inconsistent sqls and diff info into dump dir
func dumpVisitor(dsn1, dsn2 string) compare.Visitor {
	count := 0
	return func(sql string, dsn1Res compare.DsnRes, dsn2Res compare.DsnRes) error {

		info := &dumpInfo{
			num:     count,
			sql:     sql,
			dsn1:    dsn1,
			dsn2:    dsn2,
			dsn1Res: dsn1Res,
			dsn2Res: dsn2Res,
		}

		err := ioutil.WriteFile(filepath.Join(dumpDir,
			fmt.Sprintf("%d.log", count)), []byte(info.String()), os.ModePerm)
		if err != nil {
			return err
		}
		count++
		return nil
	}
}

const analyzeTemp = `%s : %d : %d : %s

example sql:

%s
`

func execAction(cmd *cobra.Command, args []string) {
	if isDirExist(dumpDir) {
		log.Fatalln("Fatal Error: dump directory already exist")
	}

	db1, err := compare.OpenDBWithRetry(dbms, dsn1)
	if err != nil {
		log.Fatalf("connect dsn1 %s error %v\n", dsn1, err)
	}

	db2, err := compare.OpenDBWithRetry(dbms, dsn2)
	if err != nil {
		log.Fatalf("connect dsn2 %s error %v\n", dsn2, err)
	}

	log.Println("Open DB ok, starting generate data in two db by ddls")

	var keyf gendata.Keyfun

	if !skipZz {
		var ddls []string
		ddls, keyf = getDdls()

		// ddls must exec without error
		errSql, err := compare.ExecSqlsInDbs(ddls, db1, db2)
		if err != nil {
			log.Printf("Fatal Error: data prepare ddl exec error %v\n", err)
			log.Fatalln(errSql)
		}

		log.Println("generating data ok")
	} else {
		keyf, err = gendata.ByDb(db1, dbms)
		if err != nil {
			log.Fatalf("Fatal Error: %v\n", err)
		}
		log.Println("skip generate data")
	}

	err = os.MkdirAll(dumpDir, os.ModePerm)
	if err != nil {
		log.Fatalf("Fatal Error: dump dir %s create fail %v\n", dumpDir, err)
	}

	log.Println("starting execute sqls generated by yy")

	visitor := dumpVisitor(dsn1, dsn2)

	if queries < 0 {
		log.Println("infinite test...")
		queries = math.MaxInt32
	}

	sqlIter := getIter(keyf)
	err = sqlIter.Visit(sqlgen.FixedTimesVisitor(func(_ int, sql string) {
		consistent, dsn1Res, dsn2Res := compare.BySql(sql, db1, db2, !order)
		if !consistent {
			visitor(sql, dsn1Res, dsn2Res)
		}
	}, queries))

	if err != nil {
		log.Fatalf("Fatal Error: %v \n", err)
	}

	log.Println("dump ok")
}

func isDirExist(path string) bool {
	s, err := os.Stat(path)
	if err != nil {
		return false
	}
	return s.IsDir()
}

// delete with red and insert with green
// res1 edit path to res2
func getColorDiff(res1, res2 string) (string, string) {
	greenColor := color.New(color.FgGreen)
	greenColor.EnableColor()
	green := greenColor.SprintFunc()
	redColor := color.New(color.FgRed)
	redColor.EnableColor()
	red := redColor.SprintFunc()
	patch := diffmatchpatch.New()
	diff := patch.DiffMain(res1, res2, false)
	var res1Buf, res2Buf bytes.Buffer
	for _, d := range diff {
		switch d.Type {
		case diffmatchpatch.DiffEqual:
			res1Buf.WriteString(d.Text)
			res2Buf.WriteString(d.Text)
		case diffmatchpatch.DiffDelete:
			res1Buf.WriteString(red(d.Text))
		case diffmatchpatch.DiffInsert:
			res2Buf.WriteString(green(d.Text))
		}
	}
	return res1Buf.String(), res2Buf.String()
}
