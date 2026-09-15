// Discovery/source evidence from the accepted #71 catalog is never admission.
// There is deliberately no flag that can turn this synthetic authority into a
// live attestation, and no router URL/provider key configuration in this probe.
export function livePreflight() {
  return {
    outcome: 'blocked_missing_live_boundary', liveRouterCalled: false, liveEligible: false, unattendedSupported: false,
    routerSource: { version: '0.5.75', commit: '17c4cc76877bd1755030a8414f8d0083f48dcccf', evidence: 'actual_pinned_modules_sqlite_synthetic_http' },
    requiredBeforeLive: ['operator-approved exact bootstrap route and complete graph', 'deployed pinned writer fencing and original-work receipt authority', 'attempt-scoped compatible Responses/tool/stream profile', 'exact Astra xhigh native request and one authorized shared initial scope', 'fresh reviews of the full consumer suite and concrete deployment packet'],
    desiredPlannerCandidates: [
      { model: 'cx/gpt-6-astra', requestedEffort: 'xhigh', outcome: 'unverified_live_capability', sourceTranslation: 'xhigh_preserved', nativeToolProtocol: 'responses_required', outputTokenLimit: 'unsupported_output_token_translation' },
      { model: 'cc/claude-opus-5', requestedEffort: 'xhigh', outcome: 'unsupported_exact_effort_translation', sourceTranslation: 'xhigh_normalized_to_high' },
      { model: 'cx/gpt-5.6-sol', requestedEffort: 'xhigh', outcome: 'unverified_live_capability', sourceTranslation: 'xhigh_preserved', outputTokenLimit: 'unsupported_output_token_translation' },
    ],
    actualRouterSyntheticProfiles: {
      route: 'gaffer-planner', consumer: 'real_PlannerSession', authority: 'accepted_shared_RouterAuthority',
      compatibleChat: 'synthetic_read_proposal_repair_receipts', nativeTranslatedChat: 'synthetic_diagnostic_only',
      consumerResponsesCodec: 'separate_native_readonly_codec_synthetic_pass', providerOutputBound: { compatible: 'synthetic_1024_cap_mapping', native: 'unsupported_output_token_translation' },
      deployedConformance: 'unverified', liveModelSettings: 'unverified', strictProviderSchema: 'unsupported',
    },
    nativeConsumerIntegration: {
      protocol: 'planner-probe-responses-read-file-v1', evidence: 'actual_pinned_router_and_staged_consumer_synthetic',
      consumerRequestBuilder: 'implemented', productionBoundaryPort: 'scoped_private_uds',
      sharedPlannerProfileAndCodec: 'closed_readonly_profile_and_exact_history',
      originalStreamAndReceiptConformance: 'synthetic_passed',
      acknowledgement: 'private_gateway_evidence_and_immutable_named_proposal',
      providerOutputTokens: null, providerMonetaryCap: null,
      strictProviderSchema: 'not_requested', localSchema: 'ajv_plus_gateway_pinned_schema_validation',
      liveInferenceAttempts: 0, liveEligible: false, issueComplete: false,
      liveGate: 'consumer_reviews_and_exact_deployment_packet_before_shared_initial_scope',
    },
    selectedSyntheticProfile: {
      route: 'gaffer-planner-fixture', profile: 'chat-text-tools-v1', protocol: 'POST /v1/chat/completions',
      stream: 'synthetic_pass', tools: 'synthetic_fixed_read_file_pass', postGenerationSchema: 'local_ajv_validation',
      responses: 'unsupported_by_boundary', reasoningEffort: 'unsupported_by_boundary', strictProviderSchema: 'unsupported_by_boundary',
      max_completion_tokens: 1024, providerUsage: 'unavailable', providerAttribution: 'unavailable',
    },
  };
}
if (import.meta.main) { console.log(JSON.stringify(livePreflight(), null, 2)); process.exitCode = 2; }
