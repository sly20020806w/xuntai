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

watch(currentId, (id) => {
  if (id === "tree" || id === "ticket" || id === "task") loadTree();
  if (id === "ticket") loadTickets();
  if (id === "task") loadTasks();
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
          <p class="note">采集池共享同一份全局配置和远端写入。告警发给当天值班的人。</p>
          <div class="split">
            <div>
              <h2 class="panel-title">采集池</h2>
              <table>
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
              <h2 class="panel-title">这个节点上的任务</h2>
              <table>
                <thead>
                  <tr><th>任务</th><th>发现</th><th>目标</th></tr>
                </thead>
                <tbody>
                  <tr v-for="row in jobRows" :key="row.name">
                    <td>{{ row.name }}</td>
                    <td>{{ row.mode }}</td>
                    <td>{{ row.target }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
          </div>
          <h2 class="panel-title">告警</h2>
          <table>
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
          <table v-if="audience === 'admin'">
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
          <p class="note">生产单停在阶段之间等人确认。开发环境不进这个表。</p>
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

        <template v-else>
          <div class="empty">
            <h2>这一层先空着</h2>
            <p>
              数据库模块的目标是 Kubernetes 上的 MySQL 和管理页面。样稿里只留位置。备份、主从和进集群的做法还没有定稿，所以这里不摆假的库表。
            </p>
          </div>
        </template>
      </section>
    </main>
  </div>
</template>
