# Likha Product Context

This glossary defines product terms used across Likha's specs. Feature behavior and delivery plans live in `specs/`.

## Language

**Model context window**:
The maximum context capacity associated with a provider/model pair for one request. It is distinct from an output-token limit.

**Input-context usage**:
The tokens in the input sent for the active conversation's most recent model request. It does not include generated output or total session spend.

**Measured usage**:
Input-token usage reported by the provider for a completed request. When a provider reports cache-read input separately, it remains part of input usage and is not added twice.

**Estimated usage**:
A local approximation of the input tokens Likha is about to send. It is always marked approximate and is not provider telemetry.

**Context tracker**:
The status-bar display of input-context usage against the model context window, when that limit is known.

**Model variant**:
A named, model-specific preset of request settings. A variant may control reasoning effort, a thinking-token budget, or whether optional thinking is enabled.

**Default variant**:
The absence of a variant preset. The provider and model's baseline settings still apply; Default does not mean thinking is disabled.

**Variant preference**:
The remembered choice for one provider/model pair. An explicit Default preference differs from having no preference.

**Composer selection**:
The provider, model, and variant selected for the next submitted work. It can differ from the selection of a run already in progress.

**Execution selection**:
The fixed provider, model, and resolved variant settings admitted for one run. Changing the composer selection does not change that run.
