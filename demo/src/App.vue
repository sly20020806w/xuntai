<script setup>
import { computed, ref, watch } from "vue";
import {
  alerts,
  apps,
  clusters,
  jobs,
  labelOf,
  machines,
  modules,
  pools,
  releases,
  roles,
  tasks,
  ticketStatus,
  tickets as seedTickets,
  tree,
  users,
  visibleRecords,
} from "./data/platform.js";

const requested = new URLSearchParams(location.search).get("m");
const currentId = ref(modules.some((item) => item.id === requested) ? requested : "tree");
const nodeId = ref("order");
const audience = ref("admin");
const ticketRows = ref(seedTickets.map((item) => ({ ...item })));
const loginName = ref("周宁");
const loginPassword = ref("xuntai-dev");
const loginError = ref("");
const session = ref(null);
const token = ref("");
const liveNodes = ref([]);
const liveMachines = ref([]);
const treeError = ref("");
const machineName = ref("");
const machineIP = ref("");
const machineNodeId = ref("");
const liveTickets = ref([]);
const ticketTemplates = ref([]);
const ticketTitle = ref("");
const ticketNodeId = ref("");
const ticketError = ref("");
const liveJobs = ref([]);
const liveScripts = ref([]);
const taskName = ref("");
const taskNodeId = ref("");
const taskScriptId = ref("");
const taskBatch = ref(1);
const taskError = ref("");
const livePools = ref([]);
const liveScrapeJobs = ref([]);
const liveRules = ref([]);
const scrapeName = ref("");
const scrapePoolId = ref("");
const scrapeNodeId = ref("");
const scrapePort = ref(9100);
const monitorError = ref("");
const liveClusters = ref([]);
const liveInstances = ref([]);
const k8sInstanceId = ref("");
const k8sImage = ref("");
const k8sTicketId = ref("");
const k8sError = ref("");
const liveReleases = ref([]);
const liveItems = ref([]);
const releaseItemId = ref("");
const releaseTag = ref("");
const releaseEnv = ref("生产");
const releaseTicketId = ref("");
const releaseError = ref("");
const liveDb = ref([]);
const liveBackups = ref([]);
const liveRestores = ref([]);
const dbNodeId = ref("");
const dbName = ref("");
const dbHost = ref("");
const dbPort = ref(3306);
const dbVersion = ref("8.0");
const dbRole = ref("从");
const dbMasterId = ref("");
const dbError = ref("");
const backupInstanceId = ref("");
const backupKind = ref("全量");
const backupKeep = ref(7);
const restoreBackupId = ref("");
const restoreInstanceId = ref("");
const restoreTicketId = ref("");

const current = computed(() => modules.find((item) => item.id === currentId.value));
const node = computed(() => tree.find((item) => item.id === nodeId.value));

const machineRows = computed(() => visibleRecords(machines, nodeId.value));
const liveLeaves = computed(() => liveNodes.value.filter((item) => item.isLeaf));
const shownMachines = computed(() => (session.value ? liveMachines.value : machineRows.value));
const shownTickets = computed(() => (session.value ? liveTickets.value : ticketView.value));
const shownTasks = computed(() => (session.value ? liveJobs.value : taskRows.value));
const taskStateText = {
  running: "执行中",
  paused: "已暂停",
  finished: "已完成",
  pending: "未开始",
};
const ticketView = computed(() => visibleRecords(ticketRows.value, nodeId.value));
const taskRows = computed(() => visibleRecords(tasks, nodeId.value));
const jobRows = computed(() => visibleRecords(jobs, nodeId.value));
const alertRows = computed(() => visibleRecords(alerts, nodeId.value));
const appRows = computed(() => visibleRecords(apps, nodeId.value));
const releaseRows = computed(() => visibleRecords(releases, nodeId.value));
const dbHosts = computed(() => liveMachines.value.filter((item) => String(item.nodeId) === dbNodeId.value));
const dbMasters = computed(() => liveDb.value.filter((item) => String(item.nodeId) === dbNodeId.value && item.role === "主"));

function approve(id) {
  const row = ticketRows.value.find((item) => item.id === id);
  if (row && row.status === "pending_approve") row.status = "pending_action";
}

async function login() {
  loginError.value = "";
  try {
    const res = await fetch("/api/base/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name: loginName.value, password: loginPassword.value }),
    });
    if (!res.ok) {
      loginError.value = "账号或密码不对";
      return;
    }
    const data = await res.json();
    const me = await fetch("/api/base/me", {
      headers: { Authorization: `Bearer ${data.token}` },
    });
    if (!me.ok) {
      loginError.value = "登录状态没有换成菜单";
      return;
    }
    token.value = data.token;
    session.value = await me.json();
    await loadTree();
    await loadTickets();
    await loadTasks();
    await loadMonitor();
    await loadK8s();
    await loadReleases();
    await loadDb();
  } catch {
    loginError.value = "登录服务没有启动";
  }
}

async function loadTree() {
  if (!token.value) return;
  treeError.value = "";
  try {
    const headers = { Authorization: `Bearer ${token.value}` };
    const [nodesRes, machinesRes] = await Promise.all([
      fetch("/api/tree/nodes", { headers }),
      fetch("/api/tree/machines", { headers }),
    ]);
    if (!nodesRes.ok || !machinesRes.ok) {
      treeError.value = "服务树没有读出来";
      return;
    }
    liveNodes.value = await nodesRes.json();
    liveMachines.value = await machinesRes.json();
    if (!liveLeaves.value.some((item) => String(item.id) === machineNodeId.value)) {
      machineNodeId.value = liveLeaves.value[0] ? String(liveLeaves.value[0].id) : "";
    }
  } catch {
    treeError.value = "登录服务没有启动";
  }
}

async function bindMachine() {
  treeError.value = "";
  if (!machineName.value.trim()) {
    treeError.value = "先写机器名";
    return;
  }
  try {
    const res = await fetch(`/api/tree/nodes/${machineNodeId.value}/machines`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${token.value}`,
      },
      body: JSON.stringify({
        name: machineName.value.trim(),
        ip: machineIP.value.trim(),
        vendor: "自建",
        spec: "",
      }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      treeError.value = data.error || "机器没有挂上";
      return;
    }
    machineName.value = "";
    machineIP.value = "";
    await loadTree();
  } catch {
    treeError.value = "登录服务没有启动";
  }
}

function ownerText(item) {
  return (item.owners || [])
    .map((owner) => owner.name + (owner.kind === "rd" ? "（研发）" : ""))
    .join("、");
}

async function loadTickets() {
  if (!token.value) return;
  ticketError.value = "";
  try {
    const headers = { Authorization: `Bearer ${token.value}` };
    const [instancesRes, templatesRes] = await Promise.all([
      fetch("/api/ticket/instances", { headers }),
      fetch("/api/ticket/templates", { headers }),
    ]);
    if (!instancesRes.ok || !templatesRes.ok) {
      ticketError.value = "工单没有读出来";
      return;
    }
    liveTickets.value = await instancesRes.json();
    ticketTemplates.value = await templatesRes.json();
    if (!liveNodes.value.length) await loadTree();
    if (!liveNodes.value.some((item) => String(item.id) === ticketNodeId.value)) {
      const order = liveNodes.value.find((item) => item.name === "订单");
      ticketNodeId.value = String((order || liveNodes.value[0] || {}).id || "");
    }
  } catch {
    ticketError.value = "登录服务没有启动";
  }
}

async function createTicket() {
  ticketError.value = "";
  const template = ticketTemplates.value[0];
  if (!template || !ticketTitle.value.trim()) {
    ticketError.value = "先写事项";
    return;
  }
  try {
    const res = await fetch("/api/ticket/instances", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${token.value}`,
      },
      body: JSON.stringify({
        templateId: template.id,
        treeNodeId: Number(ticketNodeId.value),
        title: ticketTitle.value.trim(),
      }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      ticketError.value = data.error || "工单没有建成";
      return;
    }
    ticketTitle.value = "";
    await loadTickets();
  } catch {
    ticketError.value = "登录服务没有启动";
  }
}

async function decide(id, action) {
  ticketError.value = "";
  try {
    const res = await fetch(`/api/ticket/instances/${id}/${action}`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token.value}` },
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      ticketError.value = data.error || "这张单没有改成";
      return;
    }
    await loadTickets();
  } catch {
    ticketError.value = "登录服务没有启动";
  }
}

async function loadTasks() {
  if (!token.value) return;
  taskError.value = "";
  try {
    const headers = { Authorization: `Bearer ${token.value}` };
    const [jobsRes, scriptsRes] = await Promise.all([
      fetch("/api/task/jobs", { headers }),
      fetch("/api/task/scripts", { headers }),
    ]);
    if (!jobsRes.ok || !scriptsRes.ok) {
      taskError.value = "任务没有读出来";
      return;
    }
    liveJobs.value = await jobsRes.json();
    liveScripts.value = await scriptsRes.json();
    if (!liveNodes.value.length) await loadTree();
    if (!liveNodes.value.some((item) => String(item.id) === taskNodeId.value)) {
      const observe = liveNodes.value.find((item) => item.name === "可观测");
      taskNodeId.value = String((observe || liveNodes.value[0] || {}).id || "");
    }
    if (!liveScripts.value.some((item) => String(item.ID) === taskScriptId.value)) {
      taskScriptId.value = liveScripts.value[0] ? String(liveScripts.value[0].ID) : "";
    }
  } catch {
    taskError.value = "登录服务没有启动";
  }
}

async function createTask() {
  taskError.value = "";
  if (!taskName.value.trim()) {
    taskError.value = "先写任务名";
    return;
  }
  try {
    const res = await fetch("/api/task/jobs", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${token.value}`,
      },
      body: JSON.stringify({
        name: taskName.value.trim(),
        scriptId: Number(taskScriptId.value),
        treeNodeId: Number(taskNodeId.value),
        batchSize: Number(taskBatch.value) || 1,
      }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      taskError.value = data.error || "任务没有下发";
      return;
    }
    taskName.value = "";
    await loadTasks();
  } catch {
    taskError.value = "登录服务没有启动";
  }
}

async function taskAct(id, action, body) {
  taskError.value = "";
  try {
    const res = await fetch(`/api/task/jobs/${id}/${action}`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${token.value}`,
      },
      body: body || "{}",
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      taskError.value = data.error || "任务没有改成";
      return;
    }
    await loadTasks();
  } catch {
    taskError.value = "登录服务没有启动";
  }
}

function taskBatchText(row) {
  return row.batchSize ? `每轮 ${row.batchSize} 台` : row.batch;
}

function taskProgress(row) {
  if (row.total != null && row.done != null && session.value) return `${row.done} / ${row.total}`;
  return row.progress;
}

function taskState(row) {
  return taskStateText[row.status] || row.state;
}

const alertState = { firing: "触发中", claimed: "已认领", silenced: "静默" };

async function loadMonitor() {
  if (!token.value) return;
  monitorError.value = "";
  try {
    const headers = { Authorization: `Bearer ${token.value}` };
    const [poolsRes, jobsRes, rulesRes] = await Promise.all([
      fetch("/api/monitor/pools", { headers }),
      fetch("/api/monitor/jobs", { headers }),
      fetch("/api/monitor/rules", { headers }),
    ]);
    if (!poolsRes.ok || !jobsRes.ok || !rulesRes.ok) {
      monitorError.value = "监控配置没有读出来";
      return;
    }
    livePools.value = await poolsRes.json();
    liveScrapeJobs.value = await jobsRes.json();
    liveRules.value = await rulesRes.json();
    if (!liveNodes.value.length) await loadTree();
    if (!livePools.value.some((item) => String(item.id) === scrapePoolId.value)) {
      const base = livePools.value.find((item) => item.name === "基础采集");
      scrapePoolId.value = String((base || livePools.value[0] || {}).id || "");
    }
    if (!liveLeaves.value.some((item) => String(item.id) === scrapeNodeId.value)) {
      const observe = liveLeaves.value.find((item) => item.name === "可观测");
      scrapeNodeId.value = String((observe || liveLeaves.value[0] || {}).id || "");
    }
  } catch {
    monitorError.value = "登录服务没有启动";
  }
}

async function createScrapeJob() {
  monitorError.value = "";
  if (!scrapeName.value.trim()) {
    monitorError.value = "先写任务名";
    return;
  }
  try {
    const res = await fetch("/api/monitor/jobs", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${token.value}`,
      },
      body: JSON.stringify({
        poolId: Number(scrapePoolId.value),
        treeNodeId: Number(scrapeNodeId.value),
        name: scrapeName.value.trim(),
        discover: "tree",
        port: Number(scrapePort.value),
        metricsPath: "/metrics",
      }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      monitorError.value = data.error || "采集任务没有建成";
      return;
    }
    scrapeName.value = "";
    await loadMonitor();
  } catch {
    monitorError.value = "登录服务没有启动";
  }
}

function discoverText(row) {
  if (row.discover === "tree") return "服务树";
  if (row.discover === "k8s") return "集群外";
  return row.mode;
}

function targetText(row) {
  if (row.discover === "tree") return `${row.nodeName} :${row.port}${row.metricsPath}`;
  if (row.discover === "k8s") return "集群外认证";
  return row.target;
}

async function loadK8s() {
  if (!token.value) return;
  k8sError.value = "";
  try {
    const headers = { Authorization: `Bearer ${token.value}` };
    const [clustersRes, instancesRes] = await Promise.all([
      fetch("/api/k8s/clusters", { headers }),
      fetch("/api/k8s/instances", { headers }),
    ]);
    if (!clustersRes.ok || !instancesRes.ok) {
      k8sError.value = "集群没有读出来";
      return;
    }
    liveClusters.value = await clustersRes.json();
    liveInstances.value = await instancesRes.json();
    if (!liveInstances.value.some((item) => String(item.id) === k8sInstanceId.value)) {
      const prod = liveInstances.value.find((item) => item.env === "生产");
      const picked = prod || liveInstances.value[0];
      k8sInstanceId.value = picked ? String(picked.id) : "";
      k8sImage.value = picked ? picked.image : "";
    }
  } catch {
    k8sError.value = "登录服务没有启动";
  }
}

async function saveInstance() {
  k8sError.value = "";
  const current = liveInstances.value.find((item) => String(item.id) === k8sInstanceId.value);
  if (!current || !k8sImage.value.trim()) {
    k8sError.value = "先选实例和镜像";
    return;
  }
  try {
    const res = await fetch(`/api/k8s/instances/${current.id}`, {
      method: "PUT",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${token.value}`,
      },
      body: JSON.stringify({
        image: k8sImage.value.trim(),
        replicas: current.replicas,
        ticketId: Number(k8sTicketId.value) || 0,
      }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      k8sError.value = data.error || "实例没有改成";
      return;
    }
    k8sTicketId.value = "";
    await loadK8s();
  } catch {
    k8sError.value = "登录服务没有启动";
  }
}

async function loadReleases() {
  if (!token.value) return;
  releaseError.value = "";
  try {
    const headers = { Authorization: `Bearer ${token.value}` };
    const [ordersRes, itemsRes] = await Promise.all([
      fetch("/api/cicd/orders", { headers }),
      fetch("/api/cicd/items", { headers }),
    ]);
    if (!ordersRes.ok || !itemsRes.ok) {
      releaseError.value = "发布没有读出来";
      return;
    }
    liveReleases.value = await ordersRes.json();
    liveItems.value = await itemsRes.json();
    if (!liveItems.value.some((item) => String(item.id) === releaseItemId.value)) {
      const order = liveItems.value.find((item) => item.name === "order-api");
      releaseItemId.value = String((order || liveItems.value[0] || {}).id || "");
    }
  } catch {
    releaseError.value = "登录服务没有启动";
  }
}

async function createRelease() {
  releaseError.value = "";
  if (!releaseTag.value.trim()) {
    releaseError.value = "先写标签";
    return;
  }
  try {
    const res = await fetch("/api/cicd/orders", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${token.value}`,
      },
      body: JSON.stringify({
        itemId: Number(releaseItemId.value),
        tag: releaseTag.value.trim(),
        env: releaseEnv.value,
        ticketId: Number(releaseTicketId.value) || 0,
      }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      releaseError.value = data.error || "发布没有建成";
      return;
    }
    releaseTag.value = "";
    releaseTicketId.value = "";
    await loadReleases();
  } catch {
    releaseError.value = "登录服务没有启动";
  }
}

async function confirmRelease(id) {
  releaseError.value = "";
  try {
    const res = await fetch(`/api/cicd/orders/${id}/confirm`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token.value}` },
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      releaseError.value = data.error || "阶段没有确认";
      return;
    }
    await loadReleases();
  } catch {
    releaseError.value = "登录服务没有启动";
  }
}

function stageClass(order, stage) {
  if (stage.status === "done") return "done";
  const current = (order.stages || []).find((item) => item.status === "pending");
  return current && current.name === stage.name ? "now" : "";
}

async function loadDb() {
  if (!token.value) return;
  dbError.value = "";
  try {
    const headers = { Authorization: `Bearer ${token.value}` };
    const [instancesRes, backupsRes, restoresRes] = await Promise.all([
      fetch("/api/db/instances", { headers }),
      fetch("/api/db/backups", { headers }),
      fetch("/api/db/restores", { headers }),
    ]);
    if (!instancesRes.ok || !backupsRes.ok || !restoresRes.ok) {
      dbError.value = "数据库没有读出来";
      return;
    }
    liveDb.value = await instancesRes.json();
    liveBackups.value = await backupsRes.json();
    liveRestores.value = await restoresRes.json();
    if (!liveLeaves.value.some((item) => String(item.id) === dbNodeId.value)) {
      const order = liveLeaves.value.find((item) => item.name === "订单");
      dbNodeId.value = String((order || liveLeaves.value[0] || {}).id || "");
    }
    const hosts = dbHosts.value;
    if (!hosts.some((item) => item.ip === dbHost.value)) {
      dbHost.value = hosts[0] ? hosts[0].ip : "";
    }
    const masters = dbMasters.value;
    if (!masters.some((item) => String(item.id) === dbMasterId.value)) {
      dbMasterId.value = masters[0] ? String(masters[0].id) : "";
    }
    if (!liveDb.value.some((item) => String(item.id) === backupInstanceId.value)) {
      const pay = liveDb.value.find((item) => item.name === "支付主库");
      backupInstanceId.value = String((pay || liveDb.value[0] || {}).id || "");
    }
    if (!liveBackups.value.some((item) => String(item.id) === restoreBackupId.value)) {
      restoreBackupId.value = String((liveBackups.value[0] || {}).id || "");
    }
    if (!liveDb.value.some((item) => String(item.id) === restoreInstanceId.value)) {
      const slave = liveDb.value.find((item) => item.role === "从");
      restoreInstanceId.value = String((slave || liveDb.value[0] || {}).id || "");
    }
  } catch {
    dbError.value = "登录服务没有启动";
  }
}

async function createDbInstance() {
  dbError.value = "";
  if (!dbName.value.trim()) {
    dbError.value = "先写实例名";
    return;
  }
  try {
    const res = await fetch("/api/db/instances", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${token.value}`,
      },
      body: JSON.stringify({
        name: dbName.value.trim(),
        treeNodeId: Number(dbNodeId.value),
        host: dbHost.value,
        port: Number(dbPort.value) || 3306,
        version: dbVersion.value.trim(),
        role: dbRole.value,
        masterId: dbRole.value === "从" ? Number(dbMasterId.value) || 0 : 0,
      }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      dbError.value = data.error || "实例没有建成";
      return;
    }
    dbName.value = "";
    await loadDb();
  } catch {
    dbError.value = "登录服务没有启动";
  }
}

async function createBackup() {
  dbError.value = "";
  try {
    const res = await fetch("/api/db/backups", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${token.value}`,
      },
      body: JSON.stringify({
        instanceId: Number(backupInstanceId.value),
        kind: backupKind.value,
        keep: Number(backupKeep.value) || 7,
      }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      dbError.value = data.error || "备份没有建成";
      return;
    }
    await loadDb();
  } catch {
    dbError.value = "登录服务没有启动";
  }
}

async function createRestore() {
  dbError.value = "";
  try {
    const res = await fetch("/api/db/restores", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${token.value}`,
      },
      body: JSON.stringify({
        backupId: Number(restoreBackupId.value),
        instanceId: Number(restoreInstanceId.value),
        ticketId: Number(restoreTicketId.value) || 0,
      }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      dbError.value = data.error || "还原没有建成";
      return;
    }
    restoreTicketId.value = "";
    await loadDb();
  } catch {
    dbError.value = "登录服务没有启动";
  }
}

watch(currentId, (id) => {
  if (id === "tree" || id === "ticket" || id === "task" || id === "monitor") loadTree();
  if (id === "ticket") loadTickets();
  if (id === "task") loadTasks();
  if (id === "monitor") loadMonitor();
  if (id === "k8s") loadK8s();
  if (id === "cicd") loadReleases();
  if (id === "db") loadDb();
});
</script>

<template>
  <div class="app">
    <aside class="rail">
      <div class="mark">
        <strong>巡台</strong>
        <span>样稿</span>
      </div>
      <nav aria-label="模块">
        <button
          v-for="item in modules"
          :key="item.id"
          :class="{ active: item.id === currentId }"
          @click="currentId = item.id"
        >
          <small>{{ item.index }}</small>
          <span>{{ item.name }}</span>
        </button>
      </nav>
    </aside>

    <aside class="tree">
      <header>
        <h1>节点</h1>
        <p>写操作看这里的负责人，不看菜单藏没藏。</p>
      </header>
      <ul>
        <li v-for="item in tree" :key="item.id" :class="item.depth ? 'depth-1' : ''">
          <button
            :class="{ selected: item.id === nodeId }"
            @click="nodeId = item.id"
          >
            {{ item.name }}
          </button>
        </li>
      </ul>
    </aside>

    <main class="main">
      <header>
        <div>
          <h1>{{ current.name }}</h1>
          <p>{{ current.summary }} 当前节点是{{ labelOf(nodeId) }}。</p>
        </div>
        <div class="who">
          <b>周宁</b>
          基础架构运维负责人
        </div>
      </header>

      <section class="content">
        <template v-if="currentId === 'base'">
          <form class="login" @submit.prevent="login">
            <input v-model="loginName" aria-label="姓名" />
            <input v-model="loginPassword" type="password" aria-label="密码" />
            <button type="submit">登录</button>
            <span v-if="session">{{ session.name }} 可见 {{ session.menus.map((item) => item.name).join("、") }}</span>
            <span v-else-if="loginError">{{ loginError }}</span>
          </form>
          <p class="note">菜单和接口按角色放开。林夏看不到集群节点操作。</p>
          <div class="split">
            <div>
              <h2 class="panel-title">用户</h2>
              <table>
                <thead>
                  <tr><th>姓名</th><th>角色</th><th>可见菜单</th></tr>
                </thead>
                <tbody>
                  <tr v-for="row in users" :key="row.name">
                    <td>{{ row.name }}</td>
                    <td>{{ row.roles }}</td>
                    <td>{{ row.menus }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
            <div>
              <h2 class="panel-title">角色</h2>
              <table>
                <thead>
                  <tr><th>角色</th><th>绑定</th></tr>
                </thead>
                <tbody>
                  <tr v-for="row in roles" :key="row.name">
                    <td>{{ row.name }}</td>
                    <td>{{ row.bind }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
          </div>
        </template>

        <template v-else-if="currentId === 'tree'">
          <form v-if="session" class="login" @submit.prevent="bindMachine">
            <select v-model="machineNodeId" aria-label="叶子节点">
              <option v-for="item in liveLeaves" :key="item.id" :value="String(item.id)">{{ item.name }}</option>
            </select>
            <input v-model="machineName" aria-label="机器名" placeholder="机器名" />
            <input v-model="machineIP" aria-label="地址" placeholder="地址" />
            <button type="submit">挂到叶子</button>
            <span v-if="treeError">{{ treeError }}</span>
          </form>
          <p v-if="session" class="note">只有叶子能挂机器。写操作认本节点或上级的运维负责人，研发负责人不能改。列表不按负责人收窄。</p>
          <p v-else class="note">先在底座登录。{{ node.name }}的负责人是{{ node.owners.join("、") }}。只有叶子节点能挂机器。</p>
          <h2 v-if="session" class="panel-title">节点</h2>
          <table v-if="session">
            <thead>
              <tr><th>节点</th><th>叶子</th><th>负责人</th></tr>
            </thead>
            <tbody>
              <tr v-for="item in liveNodes" :key="item.id">
                <td>{{ item.level ? "· " : "" }}{{ item.name }}</td>
                <td>{{ item.isLeaf ? "是" : "" }}</td>
                <td>{{ ownerText(item) }}</td>
              </tr>
            </tbody>
          </table>
          <h2 class="panel-title">机器</h2>
          <table>
            <thead>
              <tr><th>机器</th><th>地址</th><th>来源</th><th>规格</th><th>节点</th></tr>
            </thead>
            <tbody>
              <tr v-for="row in shownMachines" :key="row.id || row.name">
                <td>{{ row.name }}</td>
                <td>{{ row.ip }}</td>
                <td>{{ row.vendor }}</td>
                <td>{{ row.spec }}</td>
                <td>{{ row.nodeName || labelOf(row.nodeId) }}</td>
              </tr>
            </tbody>
          </table>
        </template>

        <template v-else-if="currentId === 'ticket'">
          <form v-if="session" class="login" @submit.prevent="createTicket">
            <select v-model="ticketNodeId" aria-label="节点">
              <option v-for="item in liveNodes" :key="item.id" :value="String(item.id)">{{ item.name }}</option>
            </select>
            <input v-model="ticketTitle" aria-label="事项" placeholder="事项" />
            <button type="submit">提交</button>
            <span v-if="ticketError">{{ ticketError }}</span>
          </form>
          <p v-if="session" class="note">模板没有状态。待审批只能通过或拒绝，通过以后才是待执行。不能审批自己的单。</p>
          <p v-else class="note">待审批的单可以点通过，状态会变成待执行。拒绝的单停在这里。</p>
          <table>
            <thead>
              <tr><th>单号</th><th>事项</th><th>节点</th><th>发起人</th><th>状态</th><th></th></tr>
            </thead>
            <tbody>
              <tr v-for="row in shownTickets" :key="row.id">
                <td>{{ row.id }}</td>
                <td>{{ row.title }}</td>
                <td>{{ row.nodeName || labelOf(row.nodeId) }}</td>
                <td>{{ row.applicant || row.owner }}</td>
                <td>
                  <span class="lamp" :class="ticketStatus[row.status].tone">
                    <i></i>{{ ticketStatus[row.status].text }}
                  </span>
                </td>
                <td>
                  <div class="row-actions">
                    <button
                      v-if="row.status === 'pending_approve' && !session"
                      @click="approve(row.id)"
                    >
                      通过
                    </button>
                    <button
                      v-if="row.status === 'pending_approve' && session"
                      @click="decide(row.id, 'approve')"
                    >
                      通过
                    </button>
                    <button
                      v-if="row.status === 'pending_approve' && session"
                      @click="decide(row.id, 'reject')"
                    >
                      拒绝
                    </button>
                  </div>
                </td>
              </tr>
            </tbody>
          </table>
        </template>

        <template v-else-if="currentId === 'task'">
          <form v-if="session" class="login" @submit.prevent="createTask">
            <select v-model="taskNodeId" aria-label="节点">
              <option v-for="item in liveNodes" :key="item.id" :value="String(item.id)">{{ item.name }}</option>
            </select>
            <select v-model="taskScriptId" aria-label="脚本">
              <option v-for="item in liveScripts" :key="item.ID" :value="String(item.ID)">{{ item.Name }}</option>
            </select>
            <input v-model="taskName" aria-label="任务名" placeholder="任务名" />
            <input v-model="taskBatch" aria-label="每轮台数" type="number" min="1" />
            <button type="submit">下发</button>
            <span v-if="taskError">{{ taskError }}</span>
          </form>
          <p v-if="session" class="note">同一条命令发到节点下的机器，每轮只放出并发那么多台，同一台不会再记一行。巡检是这条链路加上基线。代理还没接上，收回只是把结果记回来。</p>
          <p v-else class="note">巡检是同一条链路加上基线，不是另一套系统。</p>
          <table>
            <thead>
              <tr><th>任务</th><th>节点</th><th>并发</th><th>进度</th><th>状态</th><th v-if="session"></th></tr>
            </thead>
            <tbody>
              <tr v-for="row in shownTasks" :key="row.id || row.name">
                <td>{{ row.name }}</td>
                <td>{{ row.nodeName || labelOf(row.nodeId) }}</td>
                <td>{{ taskBatchText(row) }}</td>
                <td>{{ taskProgress(row) }}</td>
                <td>{{ taskState(row) }}</td>
                <td v-if="session">
                  <div v-if="session" class="row-actions">
                    <button
                      v-if="row.status === 'running' && row.issued && row.issued.length"
                      @click="taskAct(row.id, 'results', JSON.stringify({ hostIp: row.issued[0], status: 'success', output: '已收回' }))"
                    >
                      收回一台
                    </button>
                    <button v-if="row.status === 'running'" @click="taskAct(row.id, 'pause')">暂停</button>
                    <button v-if="row.status === 'paused'" @click="taskAct(row.id, 'resume')">继续</button>
                  </div>
                </td>
              </tr>
            </tbody>
          </table>
        </template>

        <template v-else-if="currentId === 'monitor'">
          <form v-if="session" class="login" @submit.prevent="createScrapeJob">
            <select v-model="scrapePoolId" aria-label="采集池">
              <option v-for="item in livePools" :key="item.id" :value="String(item.id)">{{ item.name }}</option>
            </select>
            <select v-model="scrapeNodeId" aria-label="叶子节点">
              <option v-for="item in liveLeaves" :key="item.id" :value="String(item.id)">{{ item.name }}</option>
            </select>
            <input v-model="scrapeName" aria-label="任务名" placeholder="任务名" />
            <input v-model="scrapePort" aria-label="端口" type="number" min="1" />
            <button type="submit">挂到叶子</button>
            <span v-if="monitorError">{{ monitorError }}</span>
          </form>
          <p v-if="session" class="note">采集池共用一份远端写入。服务树发现只挂叶子，规则用发送组编号找路由。配置从这里拉走，不往 Prometheus 推。认领和静默不在这里。</p>
          <p v-else class="note">采集池共享同一份全局配置和远端写入。告警发给当天值班的人。</p>
          <div class="split">
            <div>
              <h2 class="panel-title">采集池</h2>
              <table v-if="session">
                <thead>
                  <tr><th>池</th><th>告警</th><th>写入</th></tr>
                </thead>
                <tbody>
                  <tr v-for="row in livePools" :key="row.id">
                    <td>{{ row.name }}</td>
                    <td>{{ row.supportAlert ? "产生" : "不产生" }}</td>
                    <td>{{ row.remoteWrite }}</td>
                  </tr>
                </tbody>
              </table>
              <table v-else>
                <thead>
                  <tr><th>池</th><th>范围</th><th>采集器</th><th>写入</th></tr>
                </thead>
                <tbody>
                  <tr v-for="row in pools" :key="row.name">
                    <td>{{ row.name }}</td>
                    <td>{{ row.nodes }}</td>
                    <td>{{ row.workers }}</td>
                    <td>{{ row.write }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
            <div>
              <h2 class="panel-title">采集任务</h2>
              <table>
                <thead>
                  <tr><th>任务</th><th>发现</th><th>目标</th></tr>
                </thead>
                <tbody>
                  <tr v-for="row in (session ? liveScrapeJobs : jobRows)" :key="row.id || row.name">
                    <td>{{ row.name }}</td>
                    <td>{{ discoverText(row) }}</td>
                    <td>{{ targetText(row) }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
          </div>
          <h2 class="panel-title">告警</h2>
          <table v-if="session">
            <thead>
              <tr><th>规则</th><th>采集池</th><th>发送组</th><th>级别</th><th>状态</th></tr>
            </thead>
            <tbody>
              <tr v-for="row in liveRules" :key="row.id">
                <td>{{ row.name }}</td>
                <td>{{ row.poolName }}</td>
                <td>{{ row.sendGroupName }}</td>
                <td>{{ row.level }}</td>
                <td>{{ alertState[row.status] || "" }}</td>
              </tr>
            </tbody>
          </table>
          <table v-else>
            <thead>
              <tr><th>规则</th><th>节点</th><th>发送组</th><th>级别</th><th>状态</th></tr>
            </thead>
            <tbody>
              <tr v-for="row in alertRows" :key="row.name">
                <td>{{ row.name }}</td>
                <td>{{ labelOf(row.nodeId) }}</td>
                <td>{{ row.group }}</td>
                <td>{{ row.level }}</td>
                <td>{{ row.state }}</td>
              </tr>
            </tbody>
          </table>
        </template>

        <template v-else-if="currentId === 'k8s'">
          <div class="tabs">
            <button :class="{ active: audience === 'admin' }" @click="audience = 'admin'">
              集群管理员
            </button>
            <button :class="{ active: audience === 'app' }" @click="audience = 'app'">
              应用运维
            </button>
          </div>
          <p v-if="session" class="note">节点状态向集群查，现在还没接上，所以不在这里造节点。生产改镜像要有一张已审批的工单，开发环境不用。换镜像是再发一个标签。</p>
          <table v-if="audience === 'admin' && session">
            <thead>
              <tr><th>集群</th><th>环境</th><th>登记版本</th><th>探活</th><th>接入</th></tr>
            </thead>
            <tbody>
              <tr v-for="row in liveClusters" :key="row.id">
                <td>{{ row.name }}</td>
                <td>{{ row.env }}</td>
                <td>{{ row.version }}</td>
                <td><span class="lamp copper"><i></i>未接入</span></td>
                <td>只登记</td>
              </tr>
            </tbody>
          </table>
          <table v-else-if="audience === 'admin'">
            <thead>
              <tr><th>集群</th><th>环境</th><th>版本</th><th>探活</th><th>接入</th></tr>
            </thead>
            <tbody>
              <tr v-for="row in clusters" :key="row.name">
                <td>{{ row.name }}</td>
                <td>{{ row.env }}</td>
                <td>{{ row.version }}</td>
                <td><span class="lamp pine"><i></i>{{ row.health }}</span></td>
                <td>{{ row.kube }}</td>
              </tr>
            </tbody>
          </table>
          <template v-else-if="session">
            <form class="login" @submit.prevent="saveInstance">
              <select v-model="k8sInstanceId" aria-label="实例" @change="k8sImage = (liveInstances.find((item) => String(item.id) === k8sInstanceId) || {}).image || ''">
                <option v-for="item in liveInstances" :key="item.id" :value="String(item.id)">{{ item.appName }} · {{ item.env }}</option>
              </select>
              <input v-model="k8sImage" aria-label="镜像" placeholder="镜像" />
              <input v-model="k8sTicketId" aria-label="工单号" placeholder="生产工单号" />
              <button type="submit">改镜像</button>
              <span v-if="k8sError">{{ k8sError }}</span>
            </form>
            <table>
              <thead>
                <tr><th>应用</th><th>节点</th><th>集群</th><th>环境</th><th>镜像</th><th>副本</th></tr>
              </thead>
              <tbody>
                <tr v-for="row in liveInstances" :key="row.id">
                  <td>{{ row.appName }}</td>
                  <td>{{ row.nodeName }}</td>
                  <td>{{ row.cluster }}</td>
                  <td>{{ row.env }}</td>
                  <td>{{ row.image }}</td>
                  <td>{{ row.replicas }}</td>
                </tr>
              </tbody>
            </table>
          </template>
          <table v-else>
            <thead>
              <tr><th>应用</th><th>节点</th><th>集群</th><th>镜像</th><th>副本</th></tr>
            </thead>
            <tbody>
              <tr v-for="row in appRows" :key="row.name">
                <td>{{ row.name }}</td>
                <td>{{ labelOf(row.nodeId) }}</td>
                <td>{{ row.cluster }}</td>
                <td>{{ row.image }}</td>
                <td>{{ row.replicas }}</td>
              </tr>
            </tbody>
          </table>
        </template>

        <template v-else-if="currentId === 'cicd'">
          <form v-if="session" class="login" @submit.prevent="createRelease">
            <select v-model="releaseItemId" aria-label="发布项">
              <option v-for="item in liveItems" :key="item.id" :value="String(item.id)">{{ item.name }}</option>
            </select>
            <select v-model="releaseEnv" aria-label="环境">
              <option value="开发">开发</option>
              <option value="生产">生产</option>
            </select>
            <input v-model="releaseTag" aria-label="标签" placeholder="标签" />
            <input v-model="releaseTicketId" aria-label="工单号" placeholder="生产工单号" />
            <button type="submit">发布</button>
            <span v-if="releaseError">{{ releaseError }}</span>
          </form>
          <p v-if="session" class="note">开发环境提交后直接完成。生产停在阶段上等人确认，通过后把标签写进实例。回到旧版本是再发那个标签。</p>
          <p v-else class="note">生产单停在阶段之间等人确认。开发环境不进这个表。</p>
          <template v-if="session">
            <article v-for="row in liveReleases" :key="row.id" style="margin-bottom: 18px">
              <strong>{{ row.id }} {{ row.itemName }}</strong>
              <p class="note">{{ row.nodeName }} · {{ row.env }} · {{ row.tag }} · {{ row.status === "finished" ? "已完成" : "等待确认" }}</p>
              <div class="stages">
                <span v-for="stage in row.stages" :key="stage.seq" :class="stageClass(row, stage)">{{ stage.name }}</span>
              </div>
              <div v-if="row.status !== 'finished'" class="row-actions">
                <button @click="confirmRelease(row.id)">确认当前阶段</button>
              </div>
            </article>
          </template>
          <template v-else>
            <article v-for="row in releaseRows" :key="row.id" style="margin-bottom: 18px">
              <strong>{{ row.id }} {{ row.name }}</strong>
              <p class="note">{{ labelOf(row.nodeId) }} · {{ row.env }} · {{ row.tag }} · {{ row.stage }}</p>
              <div class="stages">
                <span class="done">构建</span>
                <span class="done">预发</span>
                <span :class="row.env === '生产' ? 'now' : 'done'">生产</span>
              </div>
            </article>
          </template>
        </template>

        <template v-else-if="currentId === 'db'">
          <template v-if="session">
            <p class="note">只登记叶子上的 MySQL。主从记的是指向关系，复制延迟不在这里造，口令也不入库。备份只接受 xtrabackup。还原要有一张已审批、并且落在同一个叶子上的工单，登记之后也不会真的去还原。</p>
            <form class="login" @submit.prevent="createDbInstance">
              <select v-model="dbNodeId" aria-label="节点" @change="dbHost = (dbHosts[0] || {}).ip || ''; dbMasterId = (dbMasters[0] || {}).id ? String(dbMasters[0].id) : ''">
                <option v-for="item in liveLeaves" :key="item.id" :value="String(item.id)">{{ item.name }}</option>
              </select>
              <input v-model="dbName" aria-label="实例名" placeholder="实例名" />
              <select v-model="dbHost" aria-label="机器">
                <option v-for="item in dbHosts" :key="item.id" :value="item.ip">{{ item.ip }}</option>
              </select>
              <input v-model="dbPort" aria-label="端口" placeholder="端口" />
              <input v-model="dbVersion" aria-label="版本" placeholder="版本" />
              <select v-model="dbRole" aria-label="角色">
                <option value="主">主</option>
                <option value="从">从</option>
              </select>
              <select v-if="dbRole === '从'" v-model="dbMasterId" aria-label="主库">
                <option v-for="item in dbMasters" :key="item.id" :value="String(item.id)">{{ item.name }}</option>
              </select>
              <button type="submit">登记实例</button>
            </form>
            <table>
              <thead>
                <tr><th>实例</th><th>节点</th><th>地址</th><th>版本</th><th>角色</th><th>主库</th><th>复制</th></tr>
              </thead>
              <tbody>
                <tr v-for="row in liveDb" :key="row.id">
                  <td>{{ row.name }}</td>
                  <td>{{ row.nodeName }}</td>
                  <td>{{ row.host }}:{{ row.port }}</td>
                  <td>{{ row.version }}</td>
                  <td>{{ row.role }}</td>
                  <td>{{ row.masterName }}</td>
                  <td><span class="lamp copper"><i></i>未接入</span></td>
                </tr>
              </tbody>
            </table>
            <form class="login" @submit.prevent="createBackup">
              <select v-model="backupInstanceId" aria-label="备份实例">
                <option v-for="item in liveDb" :key="item.id" :value="String(item.id)">{{ item.name }}</option>
              </select>
              <select v-model="backupKind" aria-label="备份类型">
                <option value="全量">全量</option>
                <option value="增量">增量</option>
              </select>
              <input v-model="backupKeep" aria-label="保留份数" placeholder="保留份数" />
              <button type="submit">登记备份</button>
            </form>
            <table>
              <thead>
                <tr><th>实例</th><th>类型</th><th>工具</th><th>保留</th><th>状态</th></tr>
              </thead>
              <tbody>
                <tr v-for="row in liveBackups" :key="row.id">
                  <td>{{ row.instanceName }}</td>
                  <td>{{ row.kind }}</td>
                  <td>{{ row.tool }}</td>
                  <td>{{ row.keep }}</td>
                  <td>{{ row.status }}</td>
                </tr>
              </tbody>
            </table>
            <form class="login" @submit.prevent="createRestore">
              <select v-model="restoreBackupId" aria-label="备份">
                <option v-for="item in liveBackups" :key="item.id" :value="String(item.id)">{{ item.instanceName }} · {{ item.kind }}</option>
              </select>
              <select v-model="restoreInstanceId" aria-label="还原到">
                <option v-for="item in liveDb" :key="item.id" :value="String(item.id)">{{ item.name }}</option>
              </select>
              <input v-model="restoreTicketId" aria-label="工单号" placeholder="工单号" />
              <button type="submit">登记还原</button>
              <span v-if="dbError">{{ dbError }}</span>
            </form>
            <table v-if="liveRestores.length">
              <thead>
                <tr><th>备份</th><th>实例</th><th>工单</th><th>状态</th></tr>
              </thead>
              <tbody>
                <tr v-for="row in liveRestores" :key="row.id">
                  <td>{{ row.backupId }}</td>
                  <td>{{ row.instanceName }}</td>
                  <td>{{ row.ticketId }}</td>
                  <td>{{ row.status }}</td>
                </tr>
              </tbody>
            </table>
          </template>
          <div v-else class="empty">
            <h2>登录后登记数据库</h2>
            <p>
              这一层只登记叶子上的 MySQL、主从指向和 xtrabackup 备份。复制延迟不在这里造。还原要等工单审批通过。代理、读写分离和放进集群，课还没讲到。
            </p>
          </div>
        </template>
      </section>
    </main>
  </div>
</template>
