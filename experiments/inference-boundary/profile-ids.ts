// Pure protocol identities: importing policy validation performs no asset reads.
export const OPENCODE_PROFILE = 'opencode-1.18.30-chat-edit-v1' as const;
export const OPENCODE_ROUTER_PROFILE = 'router-opencode-1.18.30-chat-edit-synthetic-v1' as const;
export type ProtocolProfile = 'chat-text-tools-v1' | typeof OPENCODE_PROFILE | typeof OPENCODE_ROUTER_PROFILE | 'router-chat-text-tools-synthetic-v1' | 'router-native-chat-translation-synthetic-v1';
