import { useId, type ReactNode } from "react";

interface BaseFieldProps {
  label: string;
  hint?: string;
  error?: string;
  required?: boolean;
  children?: never;
}

function Label({
  label,
  hint,
  error,
  required,
  id,
}: BaseFieldProps & { id: string }) {
  return (
    <>
      <label className="field-label" htmlFor={id}>
        {label}
        {required && <span style={{ color: "var(--danger)" }}> *</span>}
      </label>
      {hint && !error && <span className="field-hint">{hint}</span>}
      {error && <span className="field-error">{error}</span>}
    </>
  );
}

interface TextFieldProps extends BaseFieldProps {
  value: string;
  onChange: (value: string) => void;
  type?: "text" | "email" | "password" | "search" | "tel" | "url" | "date" | "number";
  placeholder?: string;
  maxLength?: number;
  autoComplete?: string;
  required?: boolean;
  autoFocus?: boolean;
  disabled?: boolean;
  readOnly?: boolean;
  /** Id of a <datalist> offering suggestions for a free-text field. */
  list?: string;
}

export function TextField({
  label,
  hint,
  error,
  required,
  value,
  onChange,
  type = "text",
  placeholder,
  autoComplete,
  disabled,
  autoFocus,
  list,
}: TextFieldProps) {
  const id = useId();
  return (
    <div className="field">
      <Label {...{ label, hint, error, required }} id={id} />
      <input
        id={id}
        className="input"
        type={type}
        value={value}
        placeholder={placeholder}
        autoComplete={autoComplete}
        list={list}
        disabled={disabled}
        autoFocus={autoFocus}
        required={required}
        aria-invalid={error ? "true" : undefined}
        aria-describedby={error ? `${id}-err` : undefined}
        onChange={(e) => onChange(e.target.value)}
      />
    </div>
  );
}

interface TextAreaFieldProps extends BaseFieldProps {
  value: string;
  onChange: (value: string) => void;
  rows?: number;
  placeholder?: string;
  maxLength?: number;
  /** Renders a live character counter; pairs with the backend max_length. */
  showCount?: boolean;
  disabled?: boolean;
  autoFocus?: boolean;
}

export function TextAreaField({
  label,
  hint,
  error,
  required,
  value,
  onChange,
  rows = 4,
  placeholder,
  maxLength,
  showCount,
  disabled,
  autoFocus,
}: TextAreaFieldProps) {
  const id = useId();
  return (
    <div className="field">
      <Label {...{ label, hint, error, required }} id={id} />
      <textarea
        id={id}
        className="textarea"
        rows={rows}
        value={value}
        placeholder={placeholder}
        maxLength={maxLength}
        disabled={disabled}
        required={required}
        autoFocus={autoFocus}
        aria-invalid={error ? "true" : undefined}
        onChange={(e) => onChange(e.target.value)}
      />
      {showCount && maxLength && (
        <span className="field-hint" style={{ textAlign: "right" }}>
          {value.length} / {maxLength}
        </span>
      )}
    </div>
  );
}

interface SelectFieldProps extends BaseFieldProps {
  value: string;
  onChange: (value: string) => void;
  options: { value: string; label: string; disabled?: boolean }[];
  placeholder?: string;
  disabled?: boolean;
}

export function SelectField({
  label,
  hint,
  error,
  required,
  value,
  onChange,
  options,
  placeholder,
  disabled,
}: SelectFieldProps) {
  const id = useId();
  return (
    <div className="field">
      <Label {...{ label, hint, error, required }} id={id} />
      <select
        id={id}
        className="select"
        value={value}
        disabled={disabled}
        required={required}
        aria-invalid={error ? "true" : undefined}
        onChange={(e) => onChange(e.target.value)}
      >
        {placeholder !== undefined && <option value="">{placeholder}</option>}
        {options.map((o) => (
          <option key={o.value} value={o.value} disabled={o.disabled}>
            {o.label}
          </option>
        ))}
      </select>
    </div>
  );
}

export function CheckboxField({
  label,
  hint,
  checked,
  onChange,
  disabled,
}: {
  label: string;
  hint?: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
  disabled?: boolean;
}) {
  return (
    <label className="checkbox" style={{ marginBottom: "var(--s3)" }}>
      <input
        type="checkbox"
        checked={checked}
        disabled={disabled}
        onChange={(e) => onChange(e.target.checked)}
      />
      <span className="checkbox-text">
        {label}
        {hint && <span className="checkbox-hint">{hint}</span>}
      </span>
    </label>
  );
}

/** Groups related fields, per the form rules in the design system. */
export function Fieldset({
  title,
  description,
  children,
}: {
  title?: string;
  description?: string;
  children: ReactNode;
}) {
  return (
    <fieldset
      style={{
        border: "none",
        padding: 0,
        margin: "0 0 var(--s5)",
        minWidth: 0,
      }}
    >
      {title && (
        <legend style={{ padding: 0, marginBottom: "var(--s1)" }}>
          <span style={{ fontSize: 13, fontWeight: 700, color: "var(--nav)" }}>{title}</span>
        </legend>
      )}
      {description && (
        <p className="small muted" style={{ marginBottom: "var(--s3)" }}>
          {description}
        </p>
      )}
      {children}
    </fieldset>
  );
}
