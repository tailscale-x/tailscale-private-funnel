import {
  Children,
  cloneElement,
  isValidElement,
  type ReactElement,
  useEffect,
  useRef,
  useState,
  type ReactNode,
  type FormEvent,
} from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
export function Icon({ name }: { name: string }) {
  return (
    <img src={`/assets/icons/${name}.svg`} alt="" width="20" height="20" />
  );
}
export function Intro({
  title,
  subtitle,
  action,
}: {
  title: string;
  subtitle: string;
  action?: ReactNode;
}) {
  return (
    <div className="pf-page-intro">
      <div>
        <p className="pf-kicker">Control plane</p>
        <h1 className="pf-title">{title}</h1>
        <p className="pf-subtitle">{subtitle}</p>
      </div>
      {action}
    </div>
  );
}
export function Panel({
  title,
  children,
}: {
  title: string;
  children: ReactNode;
}) {
  return (
    <section className="pf-surface">
      <h2>{title}</h2>
      {children}
    </section>
  );
}
export function Alert({ children }: { children: ReactNode }) {
  return (
    <p className="pf-alert pf-alert-info" role="status">
      {children}
    </p>
  );
}
export function ErrorMessage({ error }: { error: unknown }) {
  return error ? (
    <p className="pf-alert pf-alert-error" role="alert">
      {error instanceof Error ? error.message : String(error)}
    </p>
  ) : null;
}
export function Badge({ ok, label }: { ok: boolean; label: string }) {
  return (
    <span className={`pf-pill pf-pill-${ok ? "ok" : "warning"}`}>
      <i className={`pf-status-dot pf-status-${ok ? "ok" : "warning"}`} />
      {label}
    </span>
  );
}
export function Field({
  label,
  children,
  hint,
}: {
  label: string;
  children: ReactNode;
  hint?: string;
}) {
  return (
    <label className="pf-field">
      <span className="pf-label">{label}</span>
      {children}
      {hint ? <small className="pf-help">{hint}</small> : null}
    </label>
  );
}
export function Table({
  headers,
  children,
  empty,
}: {
  headers: string[];
  children: ReactNode;
  empty?: boolean;
}) {
  return (
    <div className="pf-table-wrap">
      <table className="pf-table">
        <thead>
          <tr>
            {headers.map((h) => (
              <th key={h}>{h}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {empty ? (
            <tr>
              <td colSpan={headers.length}>
                <p className="pf-empty">
                  No items yet. Use the action above to get started.
                </p>
              </td>
            </tr>
          ) : (
            Children.map(children, (row) => {
              if (
                !isValidElement<{ children?: ReactNode }>(row) ||
                row.type !== "tr"
              )
                return row;
              return cloneElement(
                row,
                {},
                Children.map(row.props.children, (cell, index) => {
                  if (!isValidElement(cell) || cell.type !== "td") return cell;
                  return cloneElement(
                    cell as ReactElement<{ "data-label"?: string }>,
                    { "data-label": headers[index] },
                  );
                }),
              );
            })
          )}
        </tbody>
      </table>
    </div>
  );
}
export function Modal({
  title,
  children,
  onClose,
}: {
  title: string;
  children: ReactNode;
  onClose: () => void;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const d = ref.current;
    d?.showModal();
    return () => {
      if (d?.open) d.close();
    };
  }, []);
  return (
    <dialog
      ref={ref}
      className="pf-react-dialog"
      onCancel={onClose}
      onClose={onClose}
    >
      <div className="pf-dialog-heading">
        <h2>{title}</h2>
        <button
          type="button"
          className="pf-secondary-button"
          onClick={onClose}
          aria-label="Close dialog"
        >
          Close
        </button>
      </div>
      {children}
    </dialog>
  );
}
export function Submit({
  pending,
  label = "Save changes",
}: {
  pending: boolean;
  label?: string;
}) {
  return (
    <button className="pf-button" disabled={pending} type="submit">
      {pending ? "Working…" : label}
    </button>
  );
}
export function JsonEditor({
  value,
  onSave,
  readOnly = false,
  onValidate,
}: {
  value: unknown;
  onSave?: (value: unknown) => Promise<unknown>;
  onValidate?: (value: unknown) => Promise<{ message: string }>;
  readOnly?: boolean;
}) {
  const text = JSON.stringify(
    value,
    (_, v) => (typeof v === "bigint" ? v.toString() : v),
    2,
  );
  const [draft, setDraft] = useState(text);
  const [validation, setValidation] = useState("");
  useEffect(() => {
    setDraft(text);
    setValidation("");
  }, [text]);
  const qc = useQueryClient();
  const mutation = useMutation({
    mutationFn: async () => {
      const parsed = JSON.parse(draft);
      return onSave?.(parsed);
    },
    onSuccess: () => qc.invalidateQueries(),
  });
  return (
    <details>
      <summary>Advanced JSON import / export</summary>
      <textarea
        aria-label="JSON document"
        className="pf-input font-mono"
        value={draft}
        onChange={(e) => {
          setDraft(e.target.value);
          setValidation("");
        }}
        rows={14}
        readOnly={readOnly}
      />
      {onSave && !readOnly ? (
        <button
          className="pf-secondary-button"
          onClick={() => mutation.mutate()}
          disabled={mutation.isPending}
        >
          Save changes
        </button>
      ) : null}
      {onValidate && !readOnly ? (
        <button
          className="pf-secondary-button"
          type="button"
          onClick={async () => {
            try {
              setValidation((await onValidate(JSON.parse(draft))).message);
            } catch (error) {
              setValidation(
                error instanceof Error ? error.message : String(error),
              );
            }
          }}
        >
          Validate only
        </button>
      ) : null}
      {validation ? <p role="status">{validation}</p> : null}
      <ErrorMessage error={mutation.error} />
    </details>
  );
}
export function formValues(e: FormEvent<HTMLFormElement>) {
  e.preventDefault();
  return new FormData(e.currentTarget);
}
export function date(value: bigint | number) {
  return Number(value) > 0
    ? new Date(Number(value) * 1000).toLocaleString()
    : "Not reported";
}
export function fresh(value: bigint | number) {
  return Number(value) > Date.now() / 1000 - 300;
}

export function auditLabel(action: string) {
  if (action.startsWith("rpc:")) {
    const method = action.split(":")[1].split(".").pop() || "Operation";
    const label = method.replace(/([a-z])([A-Z])/g, "$1 $2");
    return action.endsWith(":failed") ? `${label} · failed` : label;
  }
  const parts = action.split(":");
  const label = parts[0].replaceAll(".", " ");
  return (
    label.charAt(0).toUpperCase() +
    label.slice(1) +
    (parts.length > 2 ? ` · ${parts[parts.length - 1]}` : "")
  );
}
