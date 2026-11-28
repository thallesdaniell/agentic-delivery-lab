// Arrastar e soltar do board. A regra de quem vai para onde vem do servidor (data-moves no card, gerado
// por store.AllowedMoves); este arquivo só destaca as colunas que aceitam o card e chama o mesmo endpoint
// dos botões. O servidor confere a regra de novo: o arrastar nunca faz nada que o botão não faria.
(function () {
  "use strict";
  var dragging = null;

  // Confirmação dentro da página (<dialog>), no lugar do confirm() nativo: alguns navegadores e
  // painéis embutidos desligam os diálogos nativos, e o confirm() devolve "não" sem mostrar nada.
  function ask(question, onYes) {
    var dlg = document.getElementById("confirm");
    if (!dlg || typeof dlg.showModal !== "function") { if (window.confirm(question)) onYes(); return; }
    dlg.querySelector("#confirm-msg").textContent = question;
    dlg.returnValue = "";
    dlg.addEventListener("close", function done() {
      dlg.removeEventListener("close", done);
      if (dlg.returnValue === "ok") onYes();
    });
    dlg.showModal();
    dlg.querySelector("button[value=ok]").focus();
  }

  // Os botões com hx-confirm passam pelo mesmo diálogo.
  document.addEventListener("htmx:confirm", function (e) {
    if (!e.detail.question) return;
    e.preventDefault();
    ask(e.detail.question, function () { e.detail.issueRequest(true); });
  });

  function columns() { return document.querySelectorAll("#columns .column[data-status]"); }

  function allowed(card) { return (card.getAttribute("data-moves") || "").split(" ").filter(Boolean); }

  function clear() {
    document.body.classList.remove("dragging");
    columns().forEach(function (col) { col.classList.remove("drop-ok", "drop-no", "drop-over"); });
    if (dragging) dragging.classList.remove("is-dragging");
    dragging = null;
  }

  document.addEventListener("dragstart", function (e) {
    var card = e.target.closest && e.target.closest("article.card[draggable=true]");
    if (!card) return;
    dragging = card;
    card.classList.add("is-dragging");
    document.body.classList.add("dragging");
    e.dataTransfer.effectAllowed = "move";
    e.dataTransfer.setData("text/plain", card.getAttribute("data-card"));
    var ok = allowed(card);
    columns().forEach(function (col) {
      col.classList.add(ok.indexOf(col.getAttribute("data-status")) >= 0 ? "drop-ok" : "drop-no");
    });
  });

  document.addEventListener("dragover", function (e) {
    var col = e.target.closest && e.target.closest(".column.drop-ok");
    if (!col || !dragging) return;
    e.preventDefault(); // só as colunas da regra aceitam o card
    e.dataTransfer.dropEffect = "move";
    columns().forEach(function (c) { c.classList.toggle("drop-over", c === col); });
  });

  document.addEventListener("drop", function (e) {
    var col = e.target.closest && e.target.closest(".column.drop-ok");
    if (!col || !dragging) return;
    e.preventDefault();
    var card = dragging, to = col.getAttribute("data-status"), id = card.getAttribute("data-card");
    clear();
    var send = function () {
      window.htmx.ajax("POST", "/cards/" + encodeURIComponent(id) + "/move", {
        target: "#columns", swap: "outerHTML", values: { to: to, view: "board" }
      });
    };
    if (to !== "running") { send(); return; }
    var budget = card.getAttribute("data-budget");
    ask("Executar " + id + " com " + card.getAttribute("data-agent") + (budget ? " (teto " + budget + ")" : "") + "?", send); // gasta dinheiro
  });

  document.addEventListener("dragend", clear);

  // A atualização a cada 3 s troca as colunas inteiras; durante o arrastar, ela espera.
  document.addEventListener("htmx:beforeRequest", function (e) {
    if (dragging && e.detail.pathInfo && e.detail.pathInfo.requestPath === "/partials/columns") e.preventDefault();
  });
})();
