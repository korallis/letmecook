export const responseId = 'resp_synthetic_astra_01';
export const message = {
  id: 'msg_01', type: 'message', status: 'completed', role: 'assistant',
  phase: 'final_answer', content: [{ type: 'output_text', text: 'Hello 🌍 café', annotations: [] }],
};
export const functionOne = {
  id: 'fc_01', type: 'function_call', status: 'completed', call_id: 'call_01',
  name: 'read_file', arguments: '{"path":"one.txt"}',
};
export const functionTwo = {
  id: 'fc_02', type: 'function_call', status: 'completed', call_id: 'call_02',
  name: 'read_file', arguments: '{"path":"two.txt"}',
};
export const created = () => ({ type: 'response.created', response: { id: responseId, status: 'in_progress', output: [] } });
export const completed = (output = [message]) => ({ type: 'response.completed', response: {
  id: responseId, status: 'completed', output, error: null, incomplete_details: null, end_turn: true,
} });
export const failed = () => ({ type: 'response.failed', response: {
  id: responseId, status: 'failed', error: { code: 'server_error', message: 'Synthetic upstream failure' },
} });
export const incomplete = () => ({ type: 'response.incomplete', response: {
  id: responseId, status: 'incomplete', incomplete_details: { reason: 'max_output_tokens' },
} });
export const itemAdded = (item, index) => ({ type: 'response.output_item.added', output_index: index,
  item: { ...item, status: 'in_progress', ...(item.type === 'function_call' ? { arguments: '' } : {}) } });
export const itemDone = (item, index) => ({ type: 'response.output_item.done', output_index: index, item });
export const delta = (item, index, fragment) => ({ type: 'response.function_call_arguments.delta',
  output_index: index, item_id: item.id, delta: fragment });
export const frame = (event, { newline = '\n', named = true } = {}) =>
  `${named ? `event: ${event.type}${newline}` : ''}data: ${JSON.stringify(event)}${newline}${newline}`;
export const frames = (events, options) => events.map(event => frame(event, options)).join('');
export const nativeToolEvents = () => [
  created(), itemAdded(functionOne, 0), itemAdded(functionTwo, 1),
  delta(functionOne, 0, '{"path":'), delta(functionTwo, 1, '{"path":'),
  delta(functionOne, 0, '"one.txt"}'), delta(functionTwo, 1, '"two.txt"}'),
  itemDone(functionOne, 0), itemDone(functionTwo, 1), completed([functionOne, functionTwo]),
].map((event, sequence_number) => ({ ...event, sequence_number }));
