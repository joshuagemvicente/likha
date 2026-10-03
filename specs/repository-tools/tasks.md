# Tasks: repository tools

1. Document optional fields and legacy request decoding against the registry.
   **Done:** tool definitions expose narrowed/ranged inspection without renamed legacy tools.
2. Reuse descriptor-based confinement and unify discovery filtering with the
   existing tree ignore behavior. Add scoped traversal and cancellation inside
   walks; preserve root identity validation.
   **Done:** filters/scopes and cancellation outcomes appear in the manual guide.
3. Add directory pages, numbered file ranges, and bounded partial scan metadata.
   Bind cursors to query/root identity; expose next offset or narrow-scope advice.
   **Done:** caps and stale continuation have explicit outcomes.
4. Register the handlers as parallel-safe reads and update harness descriptions.
   **Done:** Image 2 has observed approval-free dedicated-tool behavior; Image 1
   stays refused/gated. Do not claim prompt steering is deterministic enforcement.
5. Run existing checks under [coordinator constraints](../tooling-platform/role.md);
   record missing coverage without adding tests or a code-review pass.
