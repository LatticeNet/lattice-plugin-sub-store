import { MAX_PIPELINE_OPERATORS, RAW_INPUT_LIMIT_BYTES } from "./client";

export interface FieldValidation {
  value?: string;
  error?: string;
}

export interface OperatorsValidation {
  value?: unknown[];
  error?: string;
}

/**
 * Pipeline record validators mirroring the backend's normalizePipelineRecord
 * rules (system-go): id required with the Lattice charset, name optional and
 * printable, target required and printable, operators a JSON array of at most
 * MAX_PIPELINE_OPERATORS entries.
 */
export function validatePipelineId(input: string): FieldValidation {
  const value = input.trim();
  if (!value) return { error: "Pipeline id is required" };
  if (value.length > 128 || hasControl(value)) {
    return { error: "Id is too long or contains invalid characters" };
  }
  if (!/^[A-Za-z0-9][A-Za-z0-9._-]*$/.test(value)) {
    return { error: "Start with a letter or number; use only letters, numbers, dot, underscore, or hyphen" };
  }
  return { value };
}

export function validatePipelineName(input: string): FieldValidation {
  const value = input.trim();
  if (!value) return { value: undefined };
  if (value.length > 128 || hasControl(value)) {
    return { error: "Name is too long or contains invalid characters" };
  }
  return { value };
}

export function validateTarget(input: string): FieldValidation {
  const value = input.trim();
  if (!value) return { error: "Target format is required" };
  if (value.length > 64 || hasControl(value)) {
    return { error: "Target is too long or contains invalid characters" };
  }
  return { value };
}

/** Operators arrive as a JSON array edited in a textarea; empty input means none. */
export function validateOperatorsJson(input: string): OperatorsValidation {
  const raw = input.trim();
  if (!raw) return { value: undefined };
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return { error: "Operators must be valid JSON" };
  }
  if (!Array.isArray(parsed)) {
    return { error: "Operators must be a JSON array" };
  }
  if (parsed.length > MAX_PIPELINE_OPERATORS) {
    return { error: `At most ${MAX_PIPELINE_OPERATORS} operators per pipeline` };
  }
  return { value: parsed as unknown[] };
}

export interface RawInputValidation {
  value?: string;
  error?: string;
  /** Byte length of the input — the backend caps run_pipeline raw at 1 MiB. */
  bytes: number;
  overLimit: boolean;
}

export function validateRawInput(input: string): RawInputValidation {
  const value = input.trim();
  const bytes = new TextEncoder().encode(input).length;
  if (!value) return { bytes, overLimit: false, error: "Raw subscription content is required" };
  return { value: input, bytes, overLimit: bytes > RAW_INPUT_LIMIT_BYTES };
}

function hasControl(value: string): boolean {
  return Array.from(value).some((character) => {
    const code = character.codePointAt(0) ?? 0;
    return code < 0x20 || code === 0x7f;
  });
}
