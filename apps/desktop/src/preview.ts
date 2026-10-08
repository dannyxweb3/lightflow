import type { Command, Snapshot } from './model';

// Explicit browser-only preview. Never used as a fallback for unavailable IPC.
export class PreviewClient {
  private generation = 0;
  private snapshot: Snapshot = {
    instance_id: crypto.randomUUID(),
    sequence: 0, state: 'disconnected', country_code: '', operation_id: '', simulation: true,
    daemon_version: 'browser-preview', settings: { mode: 'smart', transport_preference: 'auto', allow_lan: true },
    countries: [
      { code: 'SG', name: '新加坡', city: '新加坡', latency_ms: 38 },
      { code: 'JP', name: '日本', city: '东京', latency_ms: 52 },
      { code: 'US', name: '美国', city: '洛杉矶', latency_ms: 168 },
      { code: 'DE', name: '德国', city: '法兰克福', latency_ms: 192 },
      { code: 'AU', name: '澳大利亚', city: '悉尼', latency_ms: 126 },
      { code: 'GB', name: '英国', city: '伦敦', latency_ms: 201 },
    ],
  };
  async request(command: Command): Promise<Snapshot> {
    switch (command.method) {
      case 'connect': {
        if (this.snapshot.settings.mode === 'direct') throw new Error('DIRECT_MODE');
        if (this.snapshot.state !== 'disconnected') throw new Error('CONNECTION_BUSY');
        const country = command.params.country_code || 'SG';
        if (!this.snapshot.countries.some(c => c.code === country)) throw new Error('INVALID_COUNTRY');
        this.snapshot.country_code = country;
        this.snapshot.operation_id = crypto.randomUUID();
        this.snapshot.state = 'authorizing';
        this.snapshot.sequence++;
        const generation = ++this.generation;
        const states = ['preparing', 'connecting', 'verifying', 'connected'] as const;
        states.forEach((state, index) => setTimeout(() => {
          if (generation !== this.generation) return;
          this.snapshot.state = state;
          this.snapshot.sequence++;
          if (state === 'connected') this.snapshot.connected_at = new Date().toISOString();
        }, (index + 1) * 450));
        break;
      }
      case 'disconnect':
        this.generation++;
        this.snapshot.state = 'disconnected';
        this.snapshot.country_code = '';
        this.snapshot.operation_id = '';
        delete this.snapshot.connected_at;
        this.snapshot.sequence++;
        break;
      case 'update_settings':
        if (this.snapshot.state !== 'disconnected') throw new Error('DISCONNECT_BEFORE_SETTINGS');
        this.snapshot.settings = { ...command.params };
        this.snapshot.sequence++;
        break;
    }
    return structuredClone(this.snapshot);
  }
}
