# GitHub App do lab

O worker não usa token pessoal nem chave estática. Ele age no repositório-alvo como um **GitHub App**, e cada run recebe um token de instalação que expira em 1 hora. O App é instalado só no [`agentic-delivery-target`](https://github.com/thallesdaniell/agentic-delivery-target), com o mínimo de permissão.

## Permissões

| Permissão | Nível | Para quê |
|---|---|---|
| Metadata | Read | Obrigatória em todo App |
| Contents | Read and write | Clonar o repo e fazer push da branch do card |
| Pull requests | Read and write | Abrir o PR e ler os comentários da revisão (M4) |
| Checks | Read | Ler o resultado do CI (M4) |
| Actions | Read | Baixar o log do job que falhou para devolver ao agente (M4) |
| Workflows | **Sem acesso** | De propósito: o GitHub recusa push que altere `.github/workflows/`, então o agente não consegue mudar o CI que o avalia |

O webhook fica desligado até o M5; antes disso, o lab consulta o GitHub por polling.

## Criar o App

1. Abra o formulário já preenchido com as permissões acima:

   https://github.com/settings/apps/new?name=agentic-delivery-lab-worker&url=https://github.com/thallesdaniell/agentic-delivery-lab&public=false&webhook_active=false&contents=write&pull_requests=write&checks=read&actions=read

2. Se o nome já existir no GitHub, acrescente um sufixo. O nome do App é global.
3. Confira as permissões com a tabela acima e se "Where can this GitHub App be installed?" está em **Only on this account**.
4. Clique em **Create GitHub App** e anote o **App ID**, que aparece no topo da página do App.
5. Na mesma página, em **Private keys**, clique em **Generate a private key**. O navegador baixa um arquivo `.pem`.

## Instalar só no repositório-alvo

1. Na página do App, abra **Install App** e instale na sua conta.
2. Escolha **Only select repositories** e selecione apenas `agentic-delivery-target`.
3. Depois de instalar, a URL fica `https://github.com/settings/installations/<número>`. Esse número é o **Installation ID**.

## Guardar a chave fora do repositório

```sh
mkdir -p ~/.config/agentic-delivery-lab
mv ~/Downloads/agentic-delivery-lab-worker.*.private-key.pem ~/.config/agentic-delivery-lab/github-app.pem
chmod 600 ~/.config/agentic-delivery-lab/github-app.pem
```

Depois, copie o [`.env.example`](../.env.example) para `.env` e preencha o App ID e o Installation ID. O `.env` é ignorado pelo Git, e a chave nunca entra no repositório nem na imagem do worker: o orquestrador gera o token de instalação e injeta só o token no container.

## Depois que o App existir

Quando os PRs passarem a vir do App (M1), a branch protection do repo-alvo passa a exigir **1 aprovação**. Hoje ela exige PR e os checks `lint` e `test`, sem aprovação, porque o único autor é o dono do repositório, e o GitHub não deixa ninguém aprovar o próprio PR.
