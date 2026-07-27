import { describe, expect, it } from "vitest";

import { MAX_PIPELINE_OPERATORS, RAW_INPUT_LIMIT_BYTES } from "./client";
import {
  validateOperatorsJson,
  validatePipelineId,
  validatePipelineName,
  validateRawInput,
  validateTarget,
} from "./pipelinesModel";

describe("validatePipelineId", () => {
  it("accepts the backend charset", () => {
    expect(validatePipelineId("hk-daily").value).toBe("hk-daily");
    expect(validatePipelineId("a1._-x").value).toBe("a1._-x");
  });

  it("rejects empty, bad start, bad charset, control chars", () => {
    expect(validatePipelineId("").error).toBeTruthy();
    expect(validatePipelineId(".lead").error).toBeTruthy();
    expect(validatePipelineId("-lead").error).toBeTruthy();
    expect(validatePipelineId("has space").error).toBeTruthy();
    expect(validatePipelineId("bad\tid").error).toBeTruthy();
  });
});

describe("validatePipelineName", () => {
  it("is optional but bounded", () => {
    expect(validatePipelineName("").value).toBeUndefined();
    expect(validatePipelineName("HK daily").value).toBe("HK daily");
    expect(validatePipelineName("x".repeat(129)).error).toBeTruthy();
  });
});

describe("validateTarget", () => {
  it("requires a printable target", () => {
    expect(validateTarget("").error).toBeTruthy();
    expect(validateTarget("Clash").value).toBe("Clash");
    expect(validateTarget("x".repeat(65)).error).toBeTruthy();
  });
});

describe("validateOperatorsJson", () => {
  it("treats empty input as no operators", () => {
    expect(validateOperatorsJson("").value).toBeUndefined();
    expect(validateOperatorsJson("   ").value).toBeUndefined();
  });

  it("accepts a JSON array and rejects everything else", () => {
    expect(validateOperatorsJson('[{"type":"quick-sort"}]').value).toHaveLength(1);
    expect(validateOperatorsJson("[]").value).toEqual([]);
    expect(validateOperatorsJson("{not json").error).toBeTruthy();
    expect(validateOperatorsJson('{"type":"x"}').error).toBeTruthy();
    expect(validateOperatorsJson('"string"').error).toBeTruthy();
  });

  it("enforces the operator count cap", () => {
    const many = JSON.stringify(Array.from({ length: MAX_PIPELINE_OPERATORS + 1 }, (_, i) => ({ i })));
    expect(validateOperatorsJson(many).error).toBeTruthy();
  });
});

describe("validateRawInput", () => {
  it("requires content and reports byte size", () => {
    expect(validateRawInput("").error).toBeTruthy();
    const result = validateRawInput("ss://one\nss://two");
    expect(result.error).toBeUndefined();
    expect(result.bytes).toBeGreaterThan(0);
    expect(result.overLimit).toBe(false);
  });

  it("flags input over the backend cap", () => {
    const big = "x".repeat(RAW_INPUT_LIMIT_BYTES + 1);
    expect(validateRawInput(big).overLimit).toBe(true);
  });
});
