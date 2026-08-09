{{- define "llm.configureEnv" -}}
{{- $env := list -}}

{{- $grpcAddress := trimAll " \n\t" (default ":50051" .Values.llm.grpcAddress) -}}
{{- if $grpcAddress }}
{{- $env = append $env (dict "name" "GRPC_ADDRESS" "value" $grpcAddress) -}}
{{- end }}
{{- $authorizationAddress := trimAll " \n\t" (default "authorization:50051" .Values.llm.authorizationAddress) -}}
{{- if $authorizationAddress }}
{{- $env = append $env (dict "name" "AUTHORIZATION_ADDRESS" "value" $authorizationAddress) -}}
{{- end }}
{{- $secretsAddress := trimAll " \n\t" (default "secrets:50051" .Values.llm.secretsAddress) -}}
{{- if $secretsAddress }}
{{- $env = append $env (dict "name" "SECRETS_ADDRESS" "value" $secretsAddress) -}}
{{- end }}
{{- $agentsAddress := trimAll " \n\t" (default "agents:50051" .Values.llm.agentsAddress) -}}
{{- if $agentsAddress }}
{{- $env = append $env (dict "name" "AGENTS_ADDRESS" "value" $agentsAddress) -}}
{{- end }}
{{- $notificationsAddress := trimAll " \n\t" (default "notifications:50051" .Values.llm.notificationsAddress) -}}
{{- if $notificationsAddress }}
{{- $env = append $env (dict "name" "NOTIFICATIONS_ADDRESS" "value" $notificationsAddress) -}}
{{- end }}
{{- $dbSecret := trim (default "" .Values.llm.databaseUrl.existingSecret) -}}
{{- $dbVar := dict "name" "DATABASE_URL" -}}
{{- if $dbSecret }}
  {{- $dbKey := default "database-url" .Values.llm.databaseUrl.existingSecretKey -}}
  {{- $_ := set $dbVar "valueFrom" (dict "secretKeyRef" (dict "name" $dbSecret "key" $dbKey)) -}}
{{- else }}
  {{- $dbValue := trimAll " \n\t" (default "" .Values.llm.databaseUrl.value) -}}
  {{- $dbValue = required "llm.databaseUrl.value is required" $dbValue -}}
  {{- $_ := set $dbVar "value" $dbValue -}}
{{- end }}
{{- $env = append $env $dbVar -}}

{{- $userEnv := .Values.env | default (list) -}}
{{- $_ := set .Values "env" (concat $env $userEnv) -}}
{{- end -}}
