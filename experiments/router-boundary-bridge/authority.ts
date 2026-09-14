import { assertNativeBinding } from '../inference-boundary/native-policy.ts';
import { setTimeout as delay } from 'node:timers/promises';
import { canonical } from '../inference-boundary/json.ts';
import { Denial, type Policy } from '../inference-boundary/types.ts';
import { classifyReceipt, hashDocument, validateRouterPolicy, type RouterPolicy } from '../inference-boundary/router-policy.ts';
import type { ReceiptAuthority, Reservation } from '../inference-boundary/policy.ts';

// No writer callback or raw database capability is exposed by this interface.
export interface PrivateControl {
  state(): {boot:string;generation:number;revision:string;phase:string;policy:unknown};
  snapshot(): unknown; receipt(id:string): unknown;
  quiescent(ids:string[]): boolean; cancel(id:string): unknown;
  prepareNative?(record:Reservation):void;
  assertNativeCurrent?(record:Reservation,admission?:boolean):void;
}
export class RouterAuthority implements ReceiptAuthority {
  readonly kind = 'router-receipts-v1' as const;
  private readonly control: PrivateControl;
  private readonly approved: RouterPolicy;
  constructor(control: PrivateControl, approved: RouterPolicy) { this.control = control; this.approved = validateRouterPolicy(approved); }
  private read(id?: string) {
    // All calls are synchronous and run in the exclusive router process before
    // yielding: SQLite/request callbacks cannot interleave these observations.
    const before = this.control.state();
    const graph = this.control.snapshot();
    const receipt = id ? this.control.receipt(id) : undefined;
    const after = this.control.state();
    if (canonical(before) !== canonical(after)) throw new Denial('boundary_closed',503);
    return {state:structuredClone(after),graph:structuredClone(graph),receipt:structuredClone(receipt)};
  }
  private current(p: RouterPolicy, observation: ReturnType<RouterAuthority['read']>, admission: boolean) {
    const {state,graph} = observation;
    if (!(admission ? state.phase === 'active' : ['active','draining'].includes(state.phase)) || state.boot !== p.authority.boot || state.generation !== p.authority.generation || state.revision !== p.revision || canonical(state.policy) !== canonical(graph) || hashDocument(graph) !== p.authority.graphDigest || canonical(p) !== canonical(this.approved)) throw new Denial('boundary_closed',503);
  }
  async readFrozenPolicy(signal: AbortSignal) { signal.throwIfAborted(); this.current(this.approved,this.read(),true); return structuredClone(this.approved); }
  assertCurrent(record: Reservation, admission = false) {
    if (!record.router) throw new Denial('boundary_closed',503);
    if(record.router.policy.schema===3)assertNativeBinding(record.router.binding,record.router.policy);
    const observation = this.read(admission ? undefined : record.requestId);
    this.current(record.router.policy,observation,admission);
    if(record.router.policy.schema===3){if(!this.control.assertNativeCurrent)throw new Denial('boundary_closed',503);try{this.control.assertNativeCurrent(record,admission);}catch{throw new Denial('deadline',408);}}
    if (!admission && (!(observation.receipt as any)?.known || (observation.receipt as any).handler_done !== 1 || (observation.receipt as any).local_stop !== 'local_eof')) throw new Denial('cancelled',409);
  }
  async inspect(record: Reservation, signal: AbortSignal, recovery = false) {
    signal.throwIfAborted();
    if (!record.router || record.router.policy.authority.deploymentId !== this.approved.authority.deploymentId) throw new Denial('boundary_closed',503);
    const observation = this.read(record.requestId);
    if (!recovery) {
      this.current(record.router.policy,observation,false);
      if (['cancelled_unknown','local_cancel','local_error','crash_unknown'].includes((observation.receipt as any)?.local_stop)) throw new Denial('cancelled',409);
    }
    return classifyReceipt(observation.receipt,record.requestId,record.router);
  }
  async waitReceipt(record: Reservation, signal: AbortSignal) {
    while (true) {
      signal.throwIfAborted();
      const result = await this.inspect(record,signal);
      if (result.disposition !== 'pending_or_unknown') return result;
      await delay(5,undefined,{signal});
    }
  }
  async quiescent(ids: string[], signal: AbortSignal) {
    signal.throwIfAborted();
    // Ordinary delayed admission must not create gaffer_missing. Recovery retains
    // the boundary reservation; explicit router missing-evidence recovery is separate.
    if (ids.some(id => (this.read(id).receipt as any)?.known !== true)) return false;
    return this.control.quiescent(ids);
  }
  prepare(record:Reservation){if(record.router?.policy.schema!==3||!this.control.prepareNative)throw new Denial('boundary_closed',503);this.current(record.router.policy,this.read(),true);this.control.prepareNative(structuredClone(record));}
  cancel(id: string) { this.control.cancel(id); }
  async replaceFrozenPolicy(_next: Policy, signal: AbortSignal): Promise<Policy> { signal.throwIfAborted(); throw new Denial('replacement_read_only'); }
}
