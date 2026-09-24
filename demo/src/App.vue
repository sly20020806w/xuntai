<script setup>
import { computed, ref } from "vue";
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

const current = computed(() => modules.find((item) => item.id === currentId.value));
const node = computed(() => tree.find((item) => item.id === nodeId.value));

const machineRows = computed(() => visibleRecords(machines, nodeId.value));
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
    session.value = await me.json();
  } catch {
    loginError.value = "登录服务没有启动";
  }
}
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
          <p class="note">
            {{ node.name }}的负责人是{{ node.owners.join("、") }}。只有叶子节点能绑机器。
          </p>
          <table>
            <thead>
              <tr><th>机器</th><th>地址</th><th>来源</th><th>规格</th><th>节点</th></tr>
            </thead>
            <tbody>
              <tr v-for="row in machineRows" :key="row.name">
                <td>{{ row.name }}</td>
                <td>{{ row.ip }}</td>
                <td>{{ row.vendor }}</td>
                <td>{{ row.spec }}</td>
                <td>{{ labelOf(row.nodeId) }}</td>
              </tr>
            </tbody>
          </table>
        </template>

        <template v-else-if="currentId === 'ticket'">
          <p class="note">待审批的单可以点通过，状态会变成待执行。拒绝的单停在这里。</p>
          <table>
            <thead>
              <tr><th>单号</th><th>事项</th><th>节点</th><th>发起人</th><th>状态</th><th></th></tr>
            </thead>
            <tbody>
              <tr v-for="row in ticketView" :key="row.id">
                <td>{{ row.id }}</td>
                <td>{{ row.title }}</td>
                <td>{{ labelOf(row.nodeId) }}</td>
                <td>{{ row.owner }}</td>
                <td>
                  <span class="lamp" :class="ticketStatus[row.status].tone">
                    <i></i>{{ ticketStatus[row.status].text }}
                  </span>
                </td>
                <td>
                  <div class="row-actions">
                    <button
                      v-if="row.status === 'pending_approve'"
                      @click="approve(row.id)"
                    >
                      通过
                    </button>
                  </div>
                </td>
              </tr>
            </tbody>
          </table>
        </template>

        <template v-else-if="currentId === 'task'">
          <p class="note">巡检是同一条链路加上基线，不是另一套系统。</p>
          <table>
            <thead>
              <tr><th>任务</th><th>节点</th><th>并发</th><th>进度</th><th>状态</th></tr>
            </thead>
            <tbody>
              <tr v-for="row in taskRows" :key="row.name">
                <td>{{ row.name }}</td>
                <td>{{ labelOf(row.nodeId) }}</td>
                <td>{{ row.batch }}</td>
                <td>{{ row.progress }}</td>
                <td>{{ row.state }}</td>
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
