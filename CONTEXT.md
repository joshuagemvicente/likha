# Lisa Product Context

This glossary defines product terms used across Lisa's specs. Feature behavior and delivery plans live in `specs/`.

## Language

**Model context window**:
The maximum context capacity associated with a provider/model pair for one request. It is distinct from an output-token limit.

**Input-context usage**:
The tokens in the input sent for the active conversation's most recent model request. It does not include generated output or total session spend.

**Measured usage**:
Input-token usage reported by the provider for a completed request. When a provider reports cache-read input separately, it remains part of input usage and is not added twice.

**Estimated usage**:
A local approximation of the input tokens Lisa is about to send. It is always marked approximate and is not provider telemetry.

**Context tracker**:
The status-bar display of input-context usage against the model context window, when that limit is known.
