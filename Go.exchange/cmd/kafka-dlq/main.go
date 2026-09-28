package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"Go.exchange/config"
	"Go.exchange/dlq"
	"Go.exchange/eventing"

	"github.com/google/uuid"
)

func main() {
	code := runMain(os.Args[1:])
	os.Exit(code)
}

func runMain(args []string) int {
	applicationConfig, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runCLI(ctx, args, applicationConfig, cliDependencies{}, os.Stdout, os.Stderr)
}

type cliDependencies struct {
	reader        dlq.Reader
	openAudit     func(*config.Config) (dlq.AuditRepository, io.Closer, error)
	openPublisher func(config.KafkaConfig) (eventing.RawPublisher, io.Closer, error)
	now           func() time.Time
	newReplayID   func() string
}

type selectorOptions struct {
	consumer     string
	sourceTopic  string
	errorClass   string
	errorCode    string
	eventID      string
	failedAfter  string
	failedBefore string
	partition    int
	offset       int64
	limit        int
	scanLimit    int
	json         bool
	execute      bool
}

type listRow struct {
	DLQPartition    int       `json:"dlq_partition"`
	DLQOffset       int64     `json:"dlq_offset"`
	Consumer        string    `json:"consumer"`
	SourceTopic     string    `json:"source_topic"`
	SourcePartition int       `json:"source_partition"`
	SourceOffset    int64     `json:"source_offset"`
	EventID         string    `json:"event_id,omitempty"`
	ErrorClass      string    `json:"error_class"`
	ErrorCode       string    `json:"error_code"`
	FailedAt        time.Time `json:"failed_at"`
	Attempts        int       `json:"attempts"`
}

type listJSON struct {
	Records []listRow      `json:"records"`
	Scan    dlq.ScanResult `json:"scan"`
}

func runCLI(ctx context.Context, args []string, applicationConfig *config.Config, deps cliDependencies, out, errOut io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(errOut, "usage: kafka-dlq <list|show|replay>")
		return 1
	}
	if applicationConfig == nil {
		fmt.Fprintln(errOut, "application config is required")
		return 1
	}
	switch args[0] {
	case "list":
		return runList(ctx, args[1:], applicationConfig, deps, out, errOut)
	case "show":
		return runShow(ctx, args[1:], applicationConfig, deps, out, errOut)
	case "replay":
		return runReplay(ctx, args[1:], applicationConfig, deps, out, errOut)
	default:
		fmt.Fprintf(errOut, "unknown command %q; expected list, show, or replay\n", args[0])
		return 1
	}
}

func runList(ctx context.Context, args []string, applicationConfig *config.Config, deps cliDependencies, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(errOut)
	options := bindSelectors(fs, true, false)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(errOut, "list does not accept positional arguments")
		return 1
	}
	filter, err := options.filter()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	reader, err := getReader(applicationConfig, deps)
	if err != nil {
		fmt.Fprintf(errOut, "initialize Kafka DLQ reader: %v\n", err)
		return 1
	}
	records, scan, err := reader.Scan(ctx, filter)
	if err != nil {
		fmt.Fprintf(errOut, "scan Kafka DLQ: %v\n", err)
		return 1
	}
	rows := make([]listRow, 0, len(records))
	for _, located := range records {
		rows = append(rows, rowFor(located))
	}
	if options.json {
		if err := json.NewEncoder(out).Encode(listJSON{Records: rows, Scan: scan}); err != nil {
			fmt.Fprintf(errOut, "write list output: %v\n", err)
			return 1
		}
	} else {
		fmt.Fprintln(out, "DLQ_PARTITION\tDLQ_OFFSET\tCONSUMER\tSOURCE_TOPIC\tSOURCE_PARTITION\tSOURCE_OFFSET\tEVENT_ID\tERROR_CLASS\tERROR_CODE\tFAILED_AT\tATTEMPTS")
		for _, row := range rows {
			fmt.Fprintf(out, "%d\t%d\t%s\t%s\t%d\t%d\t%s\t%s\t%s\t%s\t%d\n",
				row.DLQPartition, row.DLQOffset, row.Consumer, row.SourceTopic, row.SourcePartition, row.SourceOffset,
				row.EventID, row.ErrorClass, row.ErrorCode, row.FailedAt.UTC().Format(time.RFC3339Nano), row.Attempts)
		}
		writeScanSummary(out, scan)
	}
	return 0
}

func runShow(ctx context.Context, args []string, applicationConfig *config.Config, deps cliDependencies, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	fs.SetOutput(errOut)
	partition := fs.Int("partition", -1, "DLQ partition")
	offset := fs.Int64("offset", -1, "DLQ offset")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 || *partition < 0 || *offset < 0 {
		fmt.Fprintln(errOut, "show requires --partition and --offset")
		return 1
	}
	reader, err := getReader(applicationConfig, deps)
	if err != nil {
		fmt.Fprintf(errOut, "initialize Kafka DLQ reader: %v\n", err)
		return 1
	}
	located, err := reader.ReadAt(ctx, *partition, *offset)
	if err != nil {
		fmt.Fprintf(errOut, "read Kafka DLQ record: %v\n", err)
		return 1
	}
	writeLocatedRecord(out, located)
	return 0
}

func runReplay(ctx context.Context, args []string, applicationConfig *config.Config, deps cliDependencies, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	fs.SetOutput(errOut)
	options := bindSelectors(fs, true, true)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(errOut, "replay does not accept positional arguments")
		return 1
	}
	filter, err := options.filter()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	single := options.partition >= 0 || options.offset >= 0
	if single && (options.partition < 0 || options.offset < 0) {
		fmt.Fprintln(errOut, "single-record replay requires both --partition and --offset")
		return 1
	}
	if single && hasReplayFilter(*options) {
		fmt.Fprintln(errOut, "single-record replay cannot be combined with batch selectors")
		return 1
	}
	if !single && strings.TrimSpace(options.consumer) == "" {
		fmt.Fprintln(errOut, "batch replay requires --consumer")
		return 1
	}
	if options.execute && !single && !hasNarrowingSelector(*options) {
		fmt.Fprintln(errOut, "refusing broad replay: add at least one narrowing selector")
		return 1
	}
	if options.execute && options.limit > dlq.MaxLimit {
		fmt.Fprintf(errOut, "batch replay limit must be at most %d\n", dlq.MaxLimit)
		return 1
	}
	reader, err := getReader(applicationConfig, deps)
	if err != nil {
		fmt.Fprintf(errOut, "initialize Kafka DLQ reader: %v\n", err)
		return 1
	}
	var records []dlq.LocatedRecord
	var scan dlq.ScanResult
	if single {
		located, readErr := reader.ReadAt(ctx, options.partition, options.offset)
		if readErr != nil {
			fmt.Fprintf(errOut, "read Kafka DLQ record: %v\n", readErr)
			return 1
		}
		records = []dlq.LocatedRecord{located}
		scan = dlq.ScanResult{Scanned: 1, Matched: 1, Returned: 1}
	} else {
		records, scan, err = reader.Scan(ctx, filter)
		if err != nil {
			fmt.Fprintf(errOut, "scan Kafka DLQ: %v\n", err)
			return 1
		}
	}
	registry, err := dlq.NewReplayPolicyRegistry(applicationConfig.Kafka)
	if err != nil {
		fmt.Fprintf(errOut, "build replay policy registry: %v\n", err)
		return 1
	}
	plans := make([]dlq.ReplayPlan, 0, len(records))
	for _, located := range records {
		plan, planErr := dlq.BuildReplayPlan(located, registry)
		if planErr != nil {
			fmt.Fprintf(errOut, "replay validation failed at partition=%d offset=%d: %v\n", located.DLQPartition, located.DLQOffset, planErr)
			return 1
		}
		plans = append(plans, plan)
	}
	if !options.execute {
		for _, plan := range plans {
			if err := json.NewEncoder(out).Encode(plan); err != nil {
				fmt.Fprintf(errOut, "write replay plan: %v\n", err)
				return 1
			}
		}
		writeScanSummary(out, scan)
		fmt.Fprintf(out, "dry-run plans=%d; no Kafka publish or audit write performed\n", len(plans))
		return 0
	}
	if len(plans) == 0 {
		fmt.Fprintln(out, "no matching DLQ records")
		writeScanSummary(out, scan)
		return 0
	}
	if deps.openAudit == nil {
		deps.openAudit = openMaintenanceAudit
	}
	if deps.openPublisher == nil {
		deps.openPublisher = openKafkaRawPublisher
	}
	audit, auditCloser, err := deps.openAudit(applicationConfig)
	if err != nil {
		fmt.Fprintf(errOut, "open replay audit database: %v\n", err)
		return 1
	}
	if auditCloser != nil {
		defer auditCloser.Close()
	}
	publisher, publisherCloser, err := deps.openPublisher(applicationConfig.Kafka)
	if err != nil {
		fmt.Fprintf(errOut, "initialize Kafka replay publisher: %v\n", err)
		return 1
	}
	if publisherCloser != nil {
		defer publisherCloser.Close()
	}
	now := deps.now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	newID := deps.newReplayID
	if newID == nil {
		newID = uuid.NewString
	}
	attempted, succeeded, unknown := 0, 0, 0
	for index, located := range records {
		startedAt := now().UTC()
		result, executeErr := dlq.ExecuteReplay(ctx, located, plans[index], newID(), startedAt, audit, publisher)
		if result.PublisherInvoked {
			attempted++
		}
		if result.Status == dlq.ReplayStatusSucceeded {
			succeeded++
		} else if result.Status == dlq.ReplayStatusUnknown {
			unknown++
		}
		fmt.Fprintf(out, "replay_id=%s dlq_topic=%s dlq_partition=%d dlq_offset=%d consumer=%s source_topic=%s source_partition=%d source_offset=%d event_id=%s error_code=%s status=%s\n",
			result.ReplayID, plans[index].DLQTopic, plans[index].DLQPartition, plans[index].DLQOffset,
			plans[index].Consumer, plans[index].SourceTopic, plans[index].SourcePartition, plans[index].SourceOffset,
			plans[index].EventID, plans[index].ErrorCode, result.Status)
		if executeErr != nil {
			fmt.Fprintf(errOut, "replay stopped: %v\n", executeErr)
			remaining := len(plans) - index - 1
			fmt.Fprintf(out, "attempted=%d succeeded=%d unknown=%d remaining=%d\n", attempted, succeeded, unknown, remaining)
			writeScanSummary(out, scan)
			if succeeded > 0 {
				return 2
			}
			return 1
		}
	}
	fmt.Fprintf(out, "attempted=%d succeeded=%d unknown=%d remaining=0\n", attempted, succeeded, unknown)
	writeScanSummary(out, scan)
	return 0
}

func bindSelectors(fs *flag.FlagSet, includeLocation, includeExecute bool) *selectorOptions {
	options := &selectorOptions{partition: -1, offset: -1, limit: dlq.DefaultLimit, scanLimit: dlq.DefaultScanLimit}
	fs.StringVar(&options.consumer, "consumer", "", "consumer name")
	fs.StringVar(&options.sourceTopic, "source-topic", "", "original source topic")
	fs.StringVar(&options.errorClass, "error-class", "", "failure class")
	fs.StringVar(&options.errorCode, "error-code", "", "stable failure code")
	fs.StringVar(&options.eventID, "event-id", "", "domain event ID")
	fs.StringVar(&options.failedAfter, "failed-after", "", "RFC3339 lower failure time")
	fs.StringVar(&options.failedBefore, "failed-before", "", "RFC3339 upper failure time")
	if includeLocation {
		fs.IntVar(&options.partition, "partition", -1, "DLQ partition")
		fs.Int64Var(&options.offset, "offset", -1, "DLQ offset")
	}
	fs.IntVar(&options.limit, "limit", dlq.DefaultLimit, "maximum matching records returned or replayed (1..500)")
	fs.IntVar(&options.scanLimit, "scan-limit", dlq.DefaultScanLimit, "maximum DLQ records inspected (1..100000)")
	if includeExecute {
		fs.BoolVar(&options.execute, "execute", false, "publish replay messages and persist audit records")
	} else {
		fs.BoolVar(&options.json, "json", false, "emit safe list metadata as JSON")
	}
	return options
}

func (o selectorOptions) filter() (dlq.Filter, error) {
	limit, scanLimit, err := normalizeLimits(o.limit, o.scanLimit)
	if err != nil {
		return dlq.Filter{}, err
	}
	filter := dlq.Filter{
		Consumer: o.consumer, SourceTopic: o.sourceTopic, ErrorClass: o.errorClass, ErrorCode: o.errorCode,
		EventID: o.eventID, Limit: limit, ScanLimit: scanLimit,
	}
	if o.partition >= 0 {
		partition := o.partition
		filter.Partition = &partition
	} else if o.partition != -1 {
		return dlq.Filter{}, errors.New("partition must be non-negative")
	}
	if o.offset >= 0 {
		offset := o.offset
		filter.Offset = &offset
	} else if o.offset != -1 {
		return dlq.Filter{}, errors.New("offset must be non-negative")
	}
	if filter.Offset != nil && filter.Partition == nil {
		return dlq.Filter{}, errors.New("--offset requires --partition")
	}
	if o.failedAfter != "" {
		parsed, err := time.Parse(time.RFC3339, o.failedAfter)
		if err != nil {
			return dlq.Filter{}, fmt.Errorf("failed-after must be RFC3339: %w", err)
		}
		parsed = parsed.UTC()
		filter.FailedAfter = &parsed
	}
	if o.failedBefore != "" {
		parsed, err := time.Parse(time.RFC3339, o.failedBefore)
		if err != nil {
			return dlq.Filter{}, fmt.Errorf("failed-before must be RFC3339: %w", err)
		}
		parsed = parsed.UTC()
		filter.FailedBefore = &parsed
	}
	if filter.FailedAfter != nil && filter.FailedBefore != nil && filter.FailedAfter.After(*filter.FailedBefore) {
		return dlq.Filter{}, errors.New("failed-after must not be later than failed-before")
	}
	return filter, nil
}

func normalizeLimits(limit, scanLimit int) (int, int, error) {
	if limit < 1 || limit > dlq.MaxLimit {
		return 0, 0, fmt.Errorf("limit must be between 1 and %d", dlq.MaxLimit)
	}
	if scanLimit < 1 || scanLimit > dlq.MaxScanLimit {
		return 0, 0, fmt.Errorf("scan-limit must be between 1 and %d", dlq.MaxScanLimit)
	}
	return limit, scanLimit, nil
}

func hasNarrowingSelector(options selectorOptions) bool {
	return options.errorCode != "" || options.eventID != "" || options.failedAfter != "" ||
		options.failedBefore != "" || options.sourceTopic != ""
}

func hasReplayFilter(options selectorOptions) bool {
	return options.consumer != "" || options.sourceTopic != "" || options.errorClass != "" || options.errorCode != "" ||
		options.eventID != "" || options.failedAfter != "" || options.failedBefore != ""
}

func rowFor(located dlq.LocatedRecord) listRow {
	return listRow{
		DLQPartition: located.DLQPartition, DLQOffset: located.DLQOffset,
		Consumer: located.Record.Consumer, SourceTopic: located.Record.Source.Topic,
		SourcePartition: located.Record.Source.Partition, SourceOffset: located.Record.Source.Offset,
		EventID: located.Record.EventID, ErrorClass: located.Record.Failure.Class, ErrorCode: located.Record.Failure.Code,
		FailedAt: located.Record.Failure.FailedAt.UTC(), Attempts: located.Record.Failure.Attempts,
	}
}

func writeScanSummary(out io.Writer, scan dlq.ScanResult) {
	fmt.Fprintf(out, "scanned=%d matched=%d returned=%d truncated=%t\n", scan.Scanned, scan.Matched, scan.Returned, scan.Truncated)
	if scan.Truncated {
		fmt.Fprintln(out, "scan limit reached; narrow the filters or increase --scan-limit")
	}
}

func writeLocatedRecord(out io.Writer, located dlq.LocatedRecord) {
	record := located.Record
	fmt.Fprintf(out, "dlq_topic=%s\ndlq_partition=%d\ndlq_offset=%d\nkafka_time=%s\n",
		located.DLQTopic, located.DLQPartition, located.DLQOffset, located.KafkaTime.UTC().Format(time.RFC3339Nano))
	fmt.Fprintf(out, "consumer=%s\nevent_id=%s\nsource_topic=%s\nsource_partition=%d\nsource_offset=%d\nsource_time=%s\n",
		record.Consumer, record.EventID, record.Source.Topic, record.Source.Partition, record.Source.Offset, record.Source.Time.UTC().Format(time.RFC3339Nano))
	fmt.Fprintf(out, "error_class=%s\nerror_code=%s\nattempts=%d\nfailed_at=%s\nreason=%s\n",
		record.Failure.Class, record.Failure.Code, record.Failure.Attempts, record.Failure.FailedAt.UTC().Format(time.RFC3339Nano), record.Failure.Reason)
	fmt.Fprintf(out, "key_base64=%s\nkey_preview=%q\n", base64.StdEncoding.EncodeToString(record.Source.Key), safePreview(record.Source.Key))
	fmt.Fprintf(out, "value_base64=%s\nvalue_preview=%q\n", base64.StdEncoding.EncodeToString(record.Source.Value), safePreview(record.Source.Value))
	fmt.Fprintf(out, "headers=%d\n", len(record.Source.Headers))
	for index, header := range record.Source.Headers {
		fmt.Fprintf(out, "header[%d].key=%q header[%d].value_base64=%s header[%d].value_preview=%q\n",
			index, header.Key, index, base64.StdEncoding.EncodeToString(header.Value), index, safePreview(header.Value))
	}
}

func safePreview(value []byte) string {
	if !utf8.Valid(value) {
		return "<non-utf8>"
	}
	const maxPreviewBytes = 256
	text := string(value)
	var builder strings.Builder
	for _, r := range text {
		if builder.Len() >= maxPreviewBytes {
			builder.WriteString("…")
			break
		}
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			builder.WriteRune('�')
		} else {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

func getReader(applicationConfig *config.Config, deps cliDependencies) (dlq.Reader, error) {
	if deps.reader != nil {
		return deps.reader, nil
	}
	return dlq.NewKafkaDLQReader(applicationConfig.Kafka)
}

func openMaintenanceAudit(applicationConfig *config.Config) (dlq.AuditRepository, io.Closer, error) {
	db, err := config.OpenMaintenanceDatabase(applicationConfig)
	if err != nil {
		return nil, nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		_ = config.CloseDatabase(db)
		return nil, nil, err
	}
	return dlq.GORMAuditRepository{DB: db}, sqlDB, nil
}

func openKafkaRawPublisher(kafkaConfig config.KafkaConfig) (eventing.RawPublisher, io.Closer, error) {
	publisher, err := eventing.NewKafkaPublisher(kafkaConfig)
	if err != nil {
		return nil, nil, err
	}
	return publisher, publisher, nil
}
