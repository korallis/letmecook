// Synthetic public HTTP only. Arbitrary header/body markers must never enter
// durable response observations. Empty HTTP 200 still has a Fetch body stream.
export const responseCases = {
  'response-json': {status:202,mediaType:'json',body:'{"synthetic_private_body":"unsupported"}'},
  'response-legacy': {status:202,mediaType:'json',body:'{"synthetic_private_body":"unsupported"}',legacy:true},
  'response-html': {status:200,mediaType:'html',body:'<html>synthetic_private_body</html>'},
  'response-missing-mime': {status:200,mediaType:'missing',body:'synthetic_private_body'},
  'response-bodyless': {status:204,mediaType:'sse',body:null},
  'response-empty-sse': {status:200,mediaType:'sse',body:''},
  'response-invalid-rejection': {status:418,mediaType:'json',body:'{"synthetic_private_body":'},
  'response-invalid-rejection-schema': {status:422,mediaType:'json',body:'{"synthetic_private_body":"no error"}'},
  'response-empty-rejection': {status:503,mediaType:'json',body:''},
  'response-sse': {status:200,mediaType:'sse',valid:true},
  'response-write': {status:200,mediaType:'sse',storageFailure:true,open:true},
  'response-rollback': {status:200,mediaType:'sse',storageFailure:true,open:true},
  'response-write-stop': {status:200,mediaType:'sse',storageFailure:true,open:true}
};
export const expectedObservation = fixture => fixture.legacy ? undefined : fixture.storageFailure ? null : {status:fixture.status,mediaType:fixture.mediaType,bodyPresent:fixture.body!==null};

export function syntheticResponse(res, fixture, validFrames) {
  const headers={'x-synthetic-private-header':'synthetic_private_header'};
  const mediaTypes={sse:'text/event-stream',json:'application/json',html:'text/html'};
  if(fixture.mediaType!=='missing')headers['content-type']=mediaTypes[fixture.mediaType]+'; synthetic_private_parameter=private';
  res.writeHead(fixture.status,headers);
  if(fixture.open){res.write(': synthetic_private_open_body\n\n');return;}
  res.end(fixture.valid?validFrames:fixture.body);
}
