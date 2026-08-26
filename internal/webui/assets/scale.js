(async function () {
  const api = async (path, options = {}) => {
    const response = await fetch(path, { ...options, headers: { 'Content-Type': 'application/json', ...(options.headers || {}) } });
    const data = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(data.error?.message || `请求失败 ${response.status}`);
    return data;
  };
  const projectID = async () => {
    const title = document.querySelector('#projectTitle')?.textContent;
    const data = await api('/api/projects');
    return data.projects?.find(p => p.title === title)?.id || '';
  };
  const revision = () => Number((document.querySelector('#projectRevision')?.textContent.match(/修订\s+(\d+)/) || [])[1] || 0);
  const actor = () => localStorage.getItem('captionflow.actor') || '制作员';
  const requestID = () => crypto.randomUUID ? crypto.randomUUID() : `req-${Date.now()}-${Math.random()}`;
  const toast = message => { const el = document.querySelector('#toast'); if (el) { el.textContent = message; el.className = 'show error'; setTimeout(() => el.className = '', 3200); } };
  const toolbar = document.querySelector('#viewDiff')?.parentElement;
  if (toolbar && !document.querySelector('#rollbackCues')) {
    const button = document.createElement('button'); button.id = 'rollbackCues'; button.className = 'secondary'; button.textContent = '回滚历史修订'; toolbar.insertBefore(button, document.querySelector('#shiftCues'));
    button.onclick = async () => {
      try {
        const id = await projectID(), current = revision(), target = Number(prompt('目标历史修订', String(Math.max(1, current - 1))));
        if (!id || !target) return;
        const preview = await api(`/api/projects/${encodeURIComponent(id)}/revisions/rollback-preview`, { method: 'POST', body: JSON.stringify({ target_revision: target, expected_revision: current }) });
        const text = preview.changes.map(c => `${c.cue_id}：${c.change_type} ${(c.changes || []).map(x => `${x.field} ${x.old_value} → ${x.new_value}`).join('；')}`).join('\n') || '字幕内容无变化';
        if (!confirm(`修订 ${current} → ${target}\n${text}\n\n确认回滚？`)) return;
        await api(`/api/projects/${encodeURIComponent(id)}/revisions/rollback`, { method: 'POST', body: JSON.stringify({ request_id: requestID(), expected_revision: current, actor: actor(), target_revision: target, confirmation_token: preview.confirmation_token }) });
        location.reload();
      } catch (e) { toast(e.message); }
    };
  }
  const reviewToolbar = document.querySelector('#batchVerify')?.parentElement;
  if (reviewToolbar && !document.querySelector('#evidenceSummary')) {
    const button = document.createElement('button'); button.id = 'evidenceSummary'; button.className = 'secondary'; button.textContent = '复验证据汇总'; reviewToolbar.insertBefore(button, document.querySelector('#addFinding'));
    button.onclick = async () => {
      try {
        const id = await projectID(), data = await api(`/api/projects/${encodeURIComponent(id)}/findings/reverification-summary?expected_revision=${revision()}`);
        const panel = document.querySelector('#findingStats'); panel.innerHTML = `<strong>复验证据汇总</strong><p>有效 ${data.valid_count} · 失效 ${data.stale_count} · 缺失 ${data.missing_count} · 可复验 ${data.eligible_finding_ids.length}</p>${data.items.map(i => `<p>${i.finding_id} · ${i.evidence_status}${i.block_reason ? ` · ${i.block_reason}` : ''}</p>`).join('')}`;
      } catch (e) { toast(e.message); }
    };
  }
})();
