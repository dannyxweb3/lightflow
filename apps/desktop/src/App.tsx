import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ArrowRight, Check, ChevronDown, CircleHelp, Cloud, Globe2, Home, LoaderCircle, LockKeyhole, Power, Search, Settings2, Shield, ShieldCheck, SlidersHorizontal, UserRound, Wifi, X } from 'lucide-react';
import { browserPreview, request } from './client';
import { errorMessage, isBusy, newest, stateLabels, type Command, type Country, type NetworkMode, type Snapshot } from './model';

type Page = 'home' | 'countries' | 'settings' | 'account' | 'diagnostics';
const nav = [
  { id: 'home', label: '连接', icon: Home }, { id: 'countries', label: '国家与地区', icon: Globe2 },
  { id: 'settings', label: '设置', icon: SlidersHorizontal },
] as const;
const modeLabels = { smart: '智能模式', global: '全局模式', direct: '直连模式' };

function Flag({ code }: { code: string }) {
  return <span aria-hidden="true" className={`flag flag-${code.toLowerCase()}`}>{({ SG: '★', JP: '●', US: '★', DE: '', AU: '✦', GB: '✚' } as Record<string, string>)[code]}</span>;
}

function GlobeArt({ active }: { active: boolean }) {
  return <div className={`globe-art ${active ? 'active' : ''}`} aria-hidden="true">
    <svg viewBox="0 0 500 370" fill="none">
      <defs><radialGradient id="globe-fill"><stop stopColor="#fffefa"/><stop offset="1" stopColor="#eee8dc"/></radialGradient><clipPath id="earth"><circle cx="250" cy="182" r="136"/></clipPath></defs>
      <ellipse cx="250" cy="328" rx="119" ry="12" fill="#d4cbb7" opacity=".17"/>
      <ellipse cx="250" cy="182" rx="215" ry="65" transform="rotate(-28 250 182)" stroke="#dfd5c3" strokeDasharray="4 6"/>
      <circle cx="250" cy="182" r="136" fill="url(#globe-fill)" stroke="#ddd4c2"/>
      <g clipPath="url(#earth)" stroke="#dbd2c2" strokeWidth=".8">
        <ellipse cx="250" cy="182" rx="49" ry="136"/><ellipse cx="250" cy="182" rx="98" ry="136"/><path d="M114 182h272M125 130h250M125 234h250M158 82h184M158 282h184"/>
        <g fill="#d3c8b2" stroke="none" opacity=".82">
          <path d="m142 118 27-25 28 3 8 13 28 7-5 21-23 13-7 28-25 5-15-18-13-9 7-17zM185 183l22 4 13 23-1 25-16 35-12-10-2-31-15-23zM254 103l24-17 43 9 6 16 25 2 24 30-16 19-23-3-17 21-27-10-9-24-17 1-20-21zM267 156l28 4 18 24-10 39-20 24-15-15-8-39zM328 245l27-6 18 20-8 13-31-5z"/>
        </g>
      </g>
      <path d="M196 117Q285 44 327 198" stroke="#e67942" strokeWidth="2" strokeDasharray="5 6" opacity=".7"/>
      <circle cx="196" cy="117" r="5" fill="#e67942"/><circle cx="327" cy="198" r="7" fill="#e67942"/>
      <circle cx="327" cy="198" r="15" stroke="#e67942" opacity=".25"/>
    </svg>
    <div className="globe-chip"><ShieldCheck size={16}/> 更简单的连接体验</div>
  </div>;
}

export default function App() {
  const queryClient = useQueryClient();
  const [page, setPage] = useState<Page>('home');
  const [selected, setSelected] = useState('');
  const [search, setSearch] = useState('');
  const [notice, setNotice] = useState('');
  const snapshot = useQuery({
    queryKey: ['snapshot'], refetchInterval: 700,
    queryFn: async () => {
      const incoming = await request({ method: 'get_snapshot', params: {} });
      const current = queryClient.getQueryData<Snapshot>(['snapshot']);
      return newest(current, incoming);
    },
  });
  const mutation = useMutation({
    mutationFn: (command: Command) => request(command),
    onSuccess: data => { queryClient.setQueryData<Snapshot>(['snapshot'], current => newest(current, data)); setNotice(''); },
    onError: error => setNotice(errorMessage(error)),
  });
  const data = snapshot.isError ? undefined : snapshot.data;
  const ready = !!data;
  const busy = isBusy(data?.state);
  const connected = data?.state === 'connected';
  const active = !!data && data.state !== 'disconnected';
  const countries = data?.countries ?? [];
  const country = countries.find(c => c.code === (active ? data.country_code : selected));
  const filtered = countries.filter(c => `${c.name} ${c.code} ${c.city}`.toLowerCase().includes(search.toLowerCase()));
  const settings = data?.settings;
  const error = notice || (snapshot.isError ? errorMessage(snapshot.error) : '');
  const disabled = !ready || mutation.isPending;

  function updateMode(mode: NetworkMode) {
    if (settings) mutation.mutate({ method: 'update_settings', params: { ...settings, mode } });
  }
  function connect() {
    mutation.mutate(active ? { method: 'disconnect', params: {} } : { method: 'connect', params: { country_code: selected } });
  }
  function countryCard(c: Country) {
    return <button key={c.code} className={`country-card ${selected === c.code ? 'selected' : ''}`} disabled={active || mutation.isPending} onClick={() => setSelected(c.code)}>
      <Flag code={c.code}/><span className="country-info"><strong>{c.name}</strong><small>{c.city}</small></span>
      <span className="latency"><i/>{c.latency_ms} ms</span>{selected === c.code ? <Check size={16}/> : <ArrowRight size={16}/>}
    </button>;
  }

  return <div className="app-shell">
    <aside className="sidebar">
      <div className="brand"><div className="brand-symbol"><Cloud size={25} strokeWidth={2.4}/></div><span>nimbus<small>VPN</small></span></div>
      <div className="workspace-label">YOUR EVERYDAY CONNECTION</div>
      <nav aria-label="主导航">{nav.map(({ id, label, icon: Icon }) => <button key={id} className={page === id ? 'nav-item current' : 'nav-item'} onClick={() => setPage(id)}><Icon size={19}/>{label}{page === id && <span className="nav-dot"/>}</button>)}</nav>
      <div className="sidebar-note"><Shield size={21}/><strong>让连接，回归简单。</strong><p>选择一个目的地，<br/>剩下的交给 Nimbus。</p></div>
      <div className="sidebar-bottom"><button className={`nav-item ${page === 'diagnostics' ? 'current' : ''}`} onClick={() => setPage('diagnostics')}><CircleHelp size={18}/>帮助与诊断</button><button className={`profile ${page === 'account' ? 'selected' : ''}`} onClick={() => setPage('account')}><span className="avatar"><UserRound size={18}/></span><span><strong>开发工作区</strong><small>尚未接入账户服务</small></span><ArrowRight size={15}/></button><span className="version">NIMBUS FOR WINDOWS · 0.1.0</span></div>
    </aside>

    <main>
      <header><div className="breadcrumb">工作区 <span>/</span> {({ home: '连接', countries: '国家与地区', settings: '设置', account: '账户', diagnostics: '诊断' })[page]}</div><span className={`service-badge ${ready ? 'online' : ''}`}><i/>{ready ? (browserPreview ? '浏览器预览' : '本地服务就绪') : '等待本地服务'}</span></header>
      <div className="page-content">
        <div className="demo-banner"><span><Shield size={15}/><strong>开发演示</strong> · 当前连接不会保护网络流量，国家及延迟为示例数据。</span><span>M1 / 客户端骨架</span></div>
        {error && <div className="error-banner" role="alert"><span>{error}</span><button onClick={() => { setNotice(''); void snapshot.refetch(); }}>重试</button></div>}

        {page === 'home' && <>
          <div className="page-heading"><div className="eyebrow">A LITTLE FREEDOM, EVERY DAY</div><h1>世界很大，连接很简单。</h1><p>选择你的目的地，开启一段自在的网络旅程。</p></div>
          <section className={`connection-panel ${connected ? 'is-connected' : ''}`}>
            <div className="connection-controls"><span className="section-label"><i/> YOUR CONNECTION</span>
              <div className={`status-tag ${connected ? 'connected' : ''}`}>{connected ? <ShieldCheck size={15}/> : <Shield size={15}/>} {ready ? stateLabels[data.state] : '服务尚未就绪'}</div>
              <h2>{connected ? '连接体验已就绪' : busy ? '为你寻找更好的连接' : '下一站，随你选择。'}</h2>
              <p className="connection-copy">{connected ? '这是演示连接。真实网络接入将在服务端联调后启用。' : '智能选择连接位置，轻松探索更大的世界。'}</p>
              <button className="destination" disabled={active || !ready} onClick={() => setPage('countries')}><span className="destination-icon">{country ? <Flag code={country.code}/> : <Globe2 size={23}/>}</span><span><small>连接目的地</small><strong>{country?.name ?? '自动选择 · 最佳位置'}</strong></span><ChevronDown size={17}/></button>
              <button className={`connect-button ${active ? 'disconnect' : ''}`} disabled={disabled || (!active && settings?.mode === 'direct')} onClick={connect}>{busy ? <LoaderCircle className="spin" size={19}/> : <Power size={19}/>} {busy ? '取消连接' : connected ? '断开演示连接' : '快速连接'}</button>
              <span className="connection-footnote"><LockKeyhole size={12}/>{connected ? '演示模式 · 网络未受保护' : '一键开启，复杂的交给我们'}</span>
            </div><GlobeArt active={connected}/>
          </section>
          <section className="mode-strip"><div className="mode-description"><span className="soft-icon"><Settings2 size={19}/></span><div><strong>适合你的网络方式</strong><small>{settings?.mode === 'global' ? '所有互联网流量通过 VPN' : settings?.mode === 'direct' ? '恢复直接访问，不建立 VPN' : '国内访问更顺畅，其他流量按需连接'}</small></div></div><div className="segmented" aria-label="网络模式">{(['smart', 'global', 'direct'] as const).map(mode => <button key={mode} disabled={disabled || active} className={settings?.mode === mode ? 'chosen' : ''} onClick={() => updateMode(mode)}>{modeLabels[mode]}</button>)}</div></section>
          <div className="section-heading"><div><h3>你的下一个目的地</h3><p>从常用位置开始，世界触手可及。</p></div><button className="text-button" onClick={() => setPage('countries')}>查看全部位置 <ArrowRight size={15}/></button></div>
          <div className="country-grid">{countries.slice(0, 3).map(countryCard)}{!ready && <div className="empty-state">本地服务启动后显示可用位置。</div>}</div>
          <div className="home-footer"><span><ShieldCheck size={14}/> 简单连接 · 自在探索</span><span>真实 VPN 尚未接入</span></div>
        </>}

        {page === 'countries' && <>
          <div className="page-heading"><div className="eyebrow">FIND YOUR NEXT DESTINATION</div><h1>下一站，去哪里？</h1><p>你选择国家，我们负责选择合适的连接。</p></div>
          <div className="country-tools"><label className="search"><Search size={18}/><input value={search} onChange={e => setSearch(e.target.value)} placeholder="搜索国家、城市或代码" aria-label="搜索国家"/>{search && <button onClick={() => setSearch('')} aria-label="清除搜索"><X size={15}/></button>}</label><button className={`auto-select ${selected === '' ? 'selected' : ''}`} disabled={active || !ready} onClick={() => setSelected('')}><Globe2 size={18}/> 自动选择{selected === '' && <Check size={16}/>}</button></div>
          {active && <p className="inline-note">请先断开，再选择新的连接位置。</p>}
          <div className="section-heading"><h3>全部位置 <span className="count">{filtered.length}</span></h3><span className="muted">示例延迟</span></div>
          <div className="country-grid all-countries">{filtered.map(countryCard)}</div>
          {!filtered.length && <div className="empty-state">{ready ? '没有找到匹配的位置，试试其他关键词。' : '请先启动本地服务。'}</div>}
          <div className="selection-summary"><span>已选择：<strong>{countries.find(c => c.code === selected)?.name ?? '自动选择'}</strong></span><button className="small-primary" onClick={() => setPage('home')}>返回连接 <ArrowRight size={16}/></button></div>
        </>}

        {page === 'settings' && <>
          <div className="page-heading"><div className="eyebrow">MAKE YOURSELF AT HOME</div><h1>按照你的方式连接。</h1><p>简单的设置，更合适的体验。演示设置仅保留于服务运行期间。</p></div>
          {active && <p className="inline-note">请先断开连接，再修改网络设置。</p>}
          <section className="settings-card"><h3>网络设置</h3><div className="setting-row"><div><strong>网络模式</strong><p>智能分流、全局连接或直接访问。</p></div><select aria-label="网络模式" value={settings?.mode ?? 'smart'} disabled={disabled || active} onChange={e => updateMode(e.target.value as NetworkMode)}>{Object.entries(modeLabels).map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></div>
            <div className="setting-row"><div><strong>连接方式</strong><p>优先顺序由本地服务内部映射。</p></div><select aria-label="连接方式" value={settings?.transport_preference ?? 'auto'} disabled={disabled || active} onChange={e => settings && mutation.mutate({ method: 'update_settings', params: { ...settings, transport_preference: e.target.value as 'auto' | 'quic' | 'tcp' } })}><option value="auto">自动</option><option value="quic">QUIC 优先</option><option value="tcp">TCP 优先</option></select></div>
            <div className="setting-row"><div><strong>允许局域网访问</strong><p>用于本地打印机、共享存储等网络资源。</p></div><button role="switch" aria-checked={settings?.allow_lan ?? false} aria-label="允许局域网访问" className={`toggle ${settings?.allow_lan ? 'on' : ''}`} disabled={disabled || active} onClick={() => settings && mutation.mutate({ method: 'update_settings', params: { ...settings, allow_lan: !settings.allow_lan } })}><span/></button></div>
            <div className="setting-row"><div><strong>断线保护 · Kill Switch</strong><p>需要 Windows 网络适配与故障恢复验证。</p></div><span className="pending-badge">尚未实现</span></div></section>
          <section className="settings-card"><h3>应用与隐私</h3><div className="setting-row"><div><strong>开机启动 / 系统托盘</strong><p>将在 Windows 服务与安装器阶段接入。</p></div><span className="pending-badge">待接入</span></div><div className="setting-row"><div><strong>产品遥测</strong><p>当前版本不上传使用数据。</p></div><span className="pending-badge">未启用</span></div></section>
        </>}

        {page === 'account' && <>
          <div className="page-heading"><div className="eyebrow">YOUR NIMBUS SPACE</div><h1>属于你的连接空间。</h1><p>账户、订阅与设备将在服务端接口同步后接入。</p></div>
          <section className="account-card"><span className="account-avatar"><UserRound size={32}/></span><h2>尚未登录</h2><p>当前处于开发工作区，不包含真实账户或套餐权益。</p><span className="pending-badge">等待服务端认证契约</span></section>
          <section className="settings-card"><h3>联调准备</h3><div className="setting-row"><div><strong>登录与设备注册</strong><p>对齐 OIDC 登录方式与设备证明。</p></div><span className="pending-badge">待接入</span></div><div className="setting-row"><div><strong>订阅与连接授权</strong><p>有效订阅与服务器临时租约是正式连接的前置条件。</p></div><span className="pending-badge">待接入</span></div></section>
        </>}

        {page === 'diagnostics' && <>
          <div className="page-heading"><div className="eyebrow">A CLEARER PICTURE</div><h1>看看连接的每一步。</h1><p>当前诊断只检查本地服务状态，不代表 VPN 或 DNS 验证通过。</p></div>
          <section className="settings-card"><h3>运行状态</h3>{[
            ['本地控制服务', ready ? '可访问' : '不可访问'], ['IPC 版本', ready ? 'v1' : '—'],
            ['服务版本', data?.daemon_version ?? '—'], ['运行模式', browserPreview ? '浏览器演示' : ready ? 'Named Pipe 开发服务' : '—'],
            ['连接状态', data ? stateLabels[data.state] : '未就绪'], ['真实网络接管', '尚未实现'], ['DNS / 路由 / 断线保护', '尚未验证'],
          ].map(([label, value]) => <div key={label} className="setting-row"><strong>{label}</strong><span className="muted">{value}</span></div>)}<button className="small-primary diagnostics-refresh" disabled={snapshot.isFetching} onClick={() => void snapshot.refetch()}><Wifi size={16}/>重新检查本地服务</button></section>
        </>}
      </div>
    </main>
  </div>;
}
