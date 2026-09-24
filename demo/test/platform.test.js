import test from "node:test";
import assert from "node:assert/strict";
import {
  modules,
  tickets,
  ticketStatus,
  visibleRecords,
  machines,
} from "../src/data/platform.js";

test("八个模块按依赖顺序排列", () => {
  assert.deepEqual(
    modules.map((item) => item.id),
    ["base", "tree", "ticket", "task", "monitor", "k8s", "cicd", "db"],
  );
  for (const item of modules) {
    assert.ok(item.name);
    assert.ok(item.summary);
  }
});

test("工单只有四种状态", () => {
  assert.deepEqual(Object.keys(ticketStatus).sort(), [
    "finished",
    "pending_action",
    "pending_approve",
    "reject",
  ]);
  for (const ticket of tickets) {
    assert.ok(ticketStatus[ticket.status]);
  }
});

test("选中叶子节点只留下该节点的机器", () => {
  const rows = visibleRecords(machines, "order");
  assert.equal(rows.length, 2);
  assert.ok(rows.every((row) => row.nodeId === "order"));
});

test("选中父节点带出它的子节点", () => {
  const rows = visibleRecords(machines, "trade");
  assert.deepEqual(
    rows.map((row) => row.nodeId).sort(),
    ["order", "order", "pay"],
  );
});
