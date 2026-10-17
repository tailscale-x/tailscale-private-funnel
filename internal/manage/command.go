// Package manage derives the management CLI from the generated RPC descriptors.
package manage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

type Options struct {
	ControlURL, Output string
	Timeout            time.Duration
	Client             *http.Client
}

func Methods() []protoreflect.MethodDescriptor {
	service := v1.File_funnel_v1_management_proto.Services().ByName("ManagementService")
	out := []protoreflect.MethodDescriptor{}
	for i := 0; i < service.Methods().Len(); i++ {
		out = append(out, service.Methods().Get(i))
	}
	return out
}

// Attach adds remote operations without changing local control lifecycle commands.
func Attach(root *cobra.Command, options *Options) {
	if options == nil {
		options = &Options{}
	}
	root.PersistentFlags().StringVar(&options.ControlURL, "control-url", "", "tailnet control URL (or XDG client.json)")
	root.PersistentFlags().StringVar(&options.Output, "output", "table", "output: table or json")
	root.PersistentFlags().DurationVar(&options.Timeout, "timeout", 30*time.Second, "RPC timeout")
	for _, method := range Methods() {
		path, _ := proto.GetExtension(method.Options(), v1.E_Cli).(string)
		if path == "" {
			continue
		}
		parts := strings.Fields(path)
		parent := root
		for _, part := range parts[:len(parts)-1] {
			var child *cobra.Command
			for _, c := range parent.Commands() {
				if c.Name() == part {
					child = c
					break
				}
			}
			if child == nil {
				child = &cobra.Command{Use: part, Short: "Manage " + part}
				parent.AddCommand(child)
			}
			parent = child
		}
		parent.AddCommand(operationCommand(method, parts[len(parts)-1], options))
	}
}
func operationCommand(method protoreflect.MethodDescriptor, name string, options *Options) *cobra.Command {
	var inputFile string
	var yes, dry bool
	doc := strings.TrimSpace(string(method.ParentFile().SourceLocations().ByDescriptor(method).LeadingComments))
	if doc == "" {
		doc = "Run " + string(method.Name())
	}
	cmd := &cobra.Command{Use: name, Short: strings.Split(doc, "\n")[0], Long: doc, Args: cobra.NoArgs, SilenceUsage: true, SilenceErrors: true}
	cmd.Flags().StringVar(&inputFile, "file", "", "protobuf JSON input file; - reads stdin")
	cmd.Flags().BoolVar(&dry, "dry-run", false, "validate input without sending a mutation")
	destructive := name == "delete" || name == "revoke" || name == "cleanup" || name == "apply"
	if destructive {
		cmd.Flags().BoolVar(&yes, "yes", false, "explicitly confirm this mutation")
	}
	fields := method.Input().Fields()
	for i := 0; i < fields.Len(); i++ {
		f := fields.Get(i)
		if secret, _ := proto.GetExtension(f.Options(), v1.E_Secret).(bool); secret {
			continue
		}
		flag := strings.ReplaceAll(string(f.Name()), "_", "-")
		if f.IsMap() || f.Kind() == protoreflect.MessageKind || f.Kind() == protoreflect.BytesKind {
			continue
		}
		if f.IsList() {
			if f.Kind() == protoreflect.StringKind {
				cmd.Flags().StringSlice(flag, nil, string(f.Name()))
			}
			continue
		}
		switch f.Kind() {
		case protoreflect.StringKind:
			cmd.Flags().String(flag, "", string(f.Name()))
		case protoreflect.BoolKind:
			cmd.Flags().Bool(flag, false, string(f.Name()))
		case protoreflect.Uint32Kind, protoreflect.Uint64Kind:
			cmd.Flags().Uint64(flag, 0, string(f.Name()))
		case protoreflect.Int32Kind, protoreflect.Int64Kind:
			cmd.Flags().Int64(flag, 0, string(f.Name()))
		}
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if destructive && !yes && !dry {
			return fmt.Errorf("%s requires --yes", name)
		}
		if options.Output != "table" && options.Output != "json" {
			return errors.New("--output must be table or json")
		}
		input := dynamicpb.NewMessage(method.Input())
		if inputFile != "" {
			var data []byte
			var err error
			if inputFile == "-" {
				data, err = io.ReadAll(io.LimitReader(cmd.InOrStdin(), 2<<20))
			} else {
				if sensitive, _ := proto.GetExtension(method.Options(), v1.E_Sensitive).(bool); sensitive {
					info, statErr := os.Stat(inputFile)
					if statErr != nil {
						return statErr
					}
					if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
						return errors.New("sensitive input requires a private regular file (chmod 600) or --file -")
					}
				}
				data, err = os.ReadFile(inputFile)
			}
			if err != nil {
				return err
			}
			if err = protojson.Unmarshal(data, input); err != nil {
				return fmt.Errorf("invalid input: %w", err)
			}
		}
		for i := 0; i < fields.Len(); i++ {
			f := fields.Get(i)
			if secret, _ := proto.GetExtension(f.Options(), v1.E_Secret).(bool); secret {
				continue
			}
			flag := strings.ReplaceAll(string(f.Name()), "_", "-")
			if cmd.Flags().Lookup(flag) == nil || !cmd.Flags().Changed(flag) {
				continue
			}
			if f.IsList() {
				values, _ := cmd.Flags().GetStringSlice(flag)
				list := input.Mutable(f).List()
				list.Truncate(0)
				for _, v := range values {
					list.Append(protoreflect.ValueOfString(v))
				}
				continue
			}
			switch f.Kind() {
			case protoreflect.StringKind:
				v, _ := cmd.Flags().GetString(flag)
				input.Set(f, protoreflect.ValueOfString(v))
			case protoreflect.BoolKind:
				v, _ := cmd.Flags().GetBool(flag)
				input.Set(f, protoreflect.ValueOfBool(v))
			case protoreflect.Uint32Kind, protoreflect.Uint64Kind:
				v, _ := cmd.Flags().GetUint64(flag)
				if f.Kind() == protoreflect.Uint32Kind {
					if v > 1<<32-1 {
						return fmt.Errorf("%s exceeds uint32", flag)
					}
					input.Set(f, protoreflect.ValueOfUint32(uint32(v)))
				} else {
					input.Set(f, protoreflect.ValueOfUint64(v))
				}
			case protoreflect.Int32Kind, protoreflect.Int64Kind:
				v, _ := cmd.Flags().GetInt64(flag)
				if f.Kind() == protoreflect.Int32Kind {
					if v < -1<<31 || v > 1<<31-1 {
						return fmt.Errorf("%s exceeds int32", flag)
					}
					input.Set(f, protoreflect.ValueOfInt32(int32(v)))
				} else {
					input.Set(f, protoreflect.ValueOfInt64(v))
				}
			}
		}
		if dry {
			if err := protovalidate.Validate(input); err != nil {
				return fmt.Errorf("invalid input: %w", err)
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "{\"dry_run\":true,\"validated\":true,\"method\":%q}\n", string(method.FullName()))
			return err
		}

		response, err := Call(cmd.Context(), method, input, *options)
		if err != nil {
			return err
		}
		data, err := (protojson.MarshalOptions{Multiline: true, Indent: "  ", EmitUnpopulated: true}).Marshal(response)
		if err != nil {
			return err
		}
		if options.Output == "json" {
			_, err = fmt.Fprintln(cmd.OutOrStdout(), string(data))
			return err
		}
		return printHuman(cmd.OutOrStdout(), response)
	}
	return cmd
}
func ResolveURL(raw string) (string, error) {
	if raw == "" {
		dir, e := os.UserConfigDir()
		if e != nil {
			return "", e
		}
		b, e := os.ReadFile(filepath.Join(dir, "tailscale-private-funnel", "client.json"))
		if e == nil {
			var c struct {
				ControlURL string `json:"control_url"`
			}
			if e = json.Unmarshal(b, &c); e != nil {
				return "", fmt.Errorf("invalid client configuration: %w", e)
			}
			raw = c.ControlURL
		} else if !os.IsNotExist(e) {
			return "", e
		}
	}
	u, e := url.Parse(raw)
	if e != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", errors.New("--control-url must be a tailnet HTTP(S) origin without credentials, query, or path")
	}
	return strings.TrimRight(raw, "/"), nil
}
func Call(ctx context.Context, method protoreflect.MethodDescriptor, input proto.Message, o Options) (proto.Message, error) {
	base, e := ResolveURL(o.ControlURL)
	if e != nil {
		return nil, e
	}
	if o.Timeout <= 0 {
		return nil, errors.New("timeout must be positive")
	}
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	client := o.Client
	if client == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		client = &http.Client{Transport: transport}
	}
	c := connect.NewClient[dynamicpb.Message, dynamicpb.Message](client, base+"/"+string(method.Parent().FullName())+"/"+string(method.Name()), connect.WithSchema(method), connect.WithResponseInitializer(func(_ connect.Spec, message any) error {
		*message.(*dynamicpb.Message) = *dynamicpb.NewMessage(method.Output())
		return nil
	}))
	response, e := c.CallUnary(ctx, connect.NewRequest(input.(*dynamicpb.Message)))
	if e != nil {
		return nil, e
	}
	return response.Msg, nil
}

// printHuman uses schema field names; it does not maintain a parallel CLI model.
func printHuman(w io.Writer, m proto.Message) error {
	b, err := (protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}).Marshal(m)
	if err != nil {
		return err
	}
	var object map[string]any
	if err = json.Unmarshal(b, &object); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	keys := func(row map[string]any) []string {
		out := []string{}
		for k := range row {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	cell := func(value any) string {
		if value == nil {
			return "—"
		}
		if text, ok := value.(string); ok {
			return strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(text)
		}
		data, _ := json.Marshal(value)
		return string(data)
	}
	if items, ok := object["items"].([]any); ok {
		if len(items) == 0 {
			_, err = fmt.Fprintln(w, "No items.")
			return err
		}
		if row, ok := items[0].(map[string]any); ok {
			columns := keys(row)
			fmt.Fprintln(tw, strings.Join(columns, "\t"))
			for _, item := range items {
				row, ok := item.(map[string]any)
				if !ok {
					return errors.New("inconsistent response rows")
				}
				values := []string{}
				for _, key := range columns {
					values = append(values, cell(row[key]))
				}
				fmt.Fprintln(tw, strings.Join(values, "\t"))
			}
			return tw.Flush()
		}
	}
	for _, key := range keys(object) {
		fmt.Fprintf(tw, "%s\t%s\n", key, cell(object[key]))
	}
	return tw.Flush()
}

// WriteError keeps machine output parseable and never includes request bodies.
func WriteError(w io.Writer, err error, output string) {
	if output == "json" {
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": connect.CodeOf(err).String(), "message": err.Error(), "exit_code": ExitCode(err)}})
		return
	}
	fmt.Fprintln(w, err)
}

// ExitCode is stable for shell agents and never relies on error text.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	switch connect.CodeOf(err) {
	case connect.CodeUnauthenticated, connect.CodePermissionDenied:
		return 3
	case connect.CodeUnavailable:
		return 4
	case connect.CodeAlreadyExists, connect.CodeFailedPrecondition, connect.CodeAborted:
		return 5
	case connect.CodeDeadlineExceeded:
		return 6
	case connect.CodeUnknown, connect.CodeInvalidArgument:
		return 2
	default:
		return 1
	}
}
