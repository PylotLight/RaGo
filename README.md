# rago
Go built AI project which uses simple open-ai function calling to interact with k8s clusters and execute commands.
![image](https://github.com/PylotLight/rago/assets/7006124/684d3318-82bb-4deb-841f-51efd90696e2)


Goals:
Short term:
The narrow focus to start with is for simple english interaction with k8s clusters using the cli via;
mods cli: https://github.com/charmbracelet/mods
e.g mods can you scale jellyfin to 0?

Long term:
- With the ability to dynamically call multiple functions, we could have a full home nas/server interaction tool for pulling server info like free memory and other logs and diagnostics for a quick evaluation tool which generates and runs the commands for you which is accessible from home or away from home via easy cli.
- I'd like to then expand this to a full k8s cluster interaction and diagnosis tool like https://github.com/k8sgpt-ai/k8sgpt
