export const responseId = 'chatcmpl_synthetic_01';
export const model = 'synthetic-chat-model';
export const chunk = (delta = {}, finish_reason = null, overrides = {}) => ({
  id: responseId, object: 'chat.completion.chunk', model,
  choices: [{ index: 0, delta, finish_reason }], ...overrides,
});
export const frame = (value, { newline = '\n', named = false } = {}) =>
  `${named ? `event: message${newline}` : ''}data: ${typeof value === 'string' ? value : JSON.stringify(value)}${newline}${newline}`;
export const frames = (values, options) => values.map(value => frame(value, options)).join('');
export const textChunks = () => [chunk({ role: 'assistant', content: '' }),
  chunk({ content: 'Hello 🌍 café' }), chunk({}, 'stop'), '[DONE]'];
export const toolStart = (index, id, name, args = '') => ({ index, id, type: 'function', function: { name, arguments: args } });
export const toolPart = (index, args) => ({ index, function: { arguments: args } });
export const toolChunks = () => [
  chunk({ role: 'assistant', content: null, tool_calls: [toolStart(0, 'call_01', 'read_file', '{"path":')] }),
  chunk({ tool_calls: [toolStart(1, 'call_02', 'read_file', '{"path":')] }),
  chunk({ tool_calls: [toolPart(1, '"two.txt"}')] }),
  chunk({ tool_calls: [toolPart(0, '"one-📄.txt"}')] }),
  chunk({}, 'tool_calls'), '[DONE]',
];
